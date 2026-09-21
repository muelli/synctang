#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 Tobias Mueller and synctang contributors
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Derive a complete Android signing identity from a single seed string.

One high-entropy seed string deterministically yields an EC P-256 private
key and a keystore passphrase; combined with the PUBLIC certificate that is
committed to git (signing/<role>-cert.pem), that is everything Android
signing needs. CI therefore requires exactly ONE secret per signing identity
instead of four (keystore blob, store password, key alias, key password).

Normal use (certificate already committed):

    SEED='...' scripts/derive-signing-key.py <role> <out.p12>

First-time bootstrap (generates the certificate; commit the .pem afterwards):

    SEED='...' scripts/derive-signing-key.py --bootstrap <role> <out.p12>

<role> is "app" or "fdroid"; it namespaces the derivation so the two
identities can never collide even if (mis)configured with the same seed.
The derived keystore passphrase is printed on stdout (nothing else is).

Why the certificate lives in git: Android identifies a signer by the exact
certificate bytes, and ECDSA self-signatures are randomized, so regenerating
the certificate each run would look like a different signer. The certificate
is public material (it is embedded in every APK), so pinning it in the
repository is both safe and transparent. The script refuses to build a
keystore whose derived key does not match the pinned certificate.

DERIVATION IS A CONTRACT (v1): the seed *is* the private key, and this
algorithm is the map between them. It must never change for existing seeds;
any future change must introduce a new version label and keep v1 intact:

  PRK        = HMAC-SHA256(key="synctang-signing-v1", msg=seed)
  scalar     = (int(HMAC-SHA512(PRK, role || ":ec-scalar")) mod (n-1)) + 1
               where n is the SECP256R1 group order; key = scalar * G
  passphrase = base64url(HMAC-SHA512(PRK, role || ":passphrase")[:24])
  alias      = the role string itself
"""
import base64
import datetime
import hashlib
import hmac
import os
import sys

from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.hazmat.primitives.serialization import pkcs12
from cryptography.x509.oid import NameOID

SALT = b"synctang-signing-v1"
COMMON_NAME = {"app": "synctang", "fdroid": "synctang F-Droid Repo"}
# SECP256R1 group order.
N = 0xFFFFFFFF00000000FFFFFFFFFFFFFFFFBCE6FAADA7179E84F3B9CAC2FC632551


def cert_path(role: str) -> str:
    return os.path.join(os.path.dirname(__file__), "..", "signing", f"{role}-cert.pem")


def main() -> int:
    args = sys.argv[1:]
    bootstrap = "--bootstrap" in args
    args = [a for a in args if a != "--bootstrap"]
    if len(args) != 2 or args[0] not in COMMON_NAME:
        sys.exit(f"usage: SEED='...' {sys.argv[0]} [--bootstrap] {{app|fdroid}} <out.p12>")
    role, out_path = args

    seed = os.environ.get("SEED", "")
    if len(seed.strip()) < 32:
        sys.exit("SEED must be set and at least 32 characters of high-entropy data")

    prk = hmac.new(SALT, seed.encode(), hashlib.sha256).digest()

    def expand(label: str) -> bytes:
        return hmac.new(prk, f"{role}:{label}".encode(), hashlib.sha512).digest()

    scalar = int.from_bytes(expand("ec-scalar"), "big") % (N - 1) + 1
    key = ec.derive_private_key(scalar, ec.SECP256R1())
    passphrase = base64.urlsafe_b64encode(expand("passphrase")[:24]).decode()

    pem = cert_path(role)
    if bootstrap:
        if os.path.exists(pem):
            sys.exit(f"{pem} already exists; refusing to overwrite an established identity")
        name = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, COMMON_NAME[role])])
        cert = (
            x509.CertificateBuilder()
            .subject_name(name)
            .issuer_name(name)
            .public_key(key.public_key())
            .serial_number(x509.random_serial_number())
            .not_valid_before(datetime.datetime(2026, 1, 1, tzinfo=datetime.timezone.utc))
            .not_valid_after(datetime.datetime(2126, 1, 1, tzinfo=datetime.timezone.utc))
            .sign(key, hashes.SHA256())
        )
        os.makedirs(os.path.dirname(pem), exist_ok=True)
        with open(pem, "wb") as f:
            f.write(cert.public_bytes(serialization.Encoding.PEM))
    else:
        try:
            with open(pem, "rb") as f:
                cert = x509.load_pem_x509_certificate(f.read())
        except FileNotFoundError:
            sys.exit(f"{pem} not found; run once with --bootstrap and commit the .pem")

    if cert.public_key().public_numbers() != key.public_key().public_numbers():
        sys.exit(
            f"SEED does not match the committed certificate {pem}: wrong seed, "
            "or the certificate belongs to a different identity"
        )

    p12 = pkcs12.serialize_key_and_certificates(
        role.encode(),
        key,
        cert,
        None,
        serialization.BestAvailableEncryption(passphrase.encode()),
    )
    with open(out_path, "wb") as f:
        f.write(p12)
    print(passphrase)
    return 0


if __name__ == "__main__":
    sys.exit(main())
