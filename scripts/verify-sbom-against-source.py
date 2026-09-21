#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Check an SBOM read out of a Go binary against the source it was built from.

    scripts/verify-sbom-against-source.py <sbom.cdx.json> <package> [GOOS GOARCH]

The SBOMs in this project are read out of the compiled artefact, so they
describe what was actually linked rather than what go.mod declared. The
obvious objection is that this trusts one tool's reading of one binary:
if syft misparsed the build info, or the binary had been stripped of it,
or the wrong file were scanned, the result would be a confidently wrong
SBOM rather than a visible failure.

So this asks the compiler the same question independently. "go list
-deps" resolves the package import graph from source, which is where the
linker's own module list ultimately comes from, and the two are compared.
They should agree exactly, and a divergence means one of them is wrong
and neither should be published.

Three differences are expected rather than suspicious:

  * The standard library appears in the binary (as "stdlib", carrying the
    toolchain version) and is not a module, so "go list -deps" does not
    report it that way. It is dropped before comparing. Nothing else is
    dropped: an unexplained absence must fail.
  * The main module is the artefact, not a dependency of it.
  * A replaced module is reported by "go list" under its declared path
    and by the binary under the path it was replaced with. This is not
    hypothetical here: syncthing-socket declares a bare "module
    syncthing-socket", so source analysis alone yields a component name
    no purl can resolve to a real project, which is one concrete way the
    binary is the more accurate of the two. Replacements are read from
    go.mod rather than hard-coded.
"""

import json
import subprocess
import sys


def go_list_modules(package: str, goos: str | None, goarch: str | None) -> set[str]:
    """Module path@version for every module contributing a package."""
    env_prefix = []
    if goos:
        env_prefix += [f"GOOS={goos}"]
    if goarch:
        env_prefix += [f"GOARCH={goarch}"]

    cmd = ["go", "list", "-deps", "-f",
           "{{if .Module}}{{.Module.Path}}@{{.Module.Version}}{{end}}", package]
    if env_prefix:
        cmd = ["env"] + env_prefix + cmd

    out = subprocess.run(cmd, capture_output=True, text=True, check=True).stdout
    return {line.strip() for line in out.splitlines() if line.strip()}


def replacements() -> dict[str, str]:
    """Declared module path -> the path it is replaced with."""
    out = subprocess.run(["go", "mod", "edit", "-json"],
                         capture_output=True, text=True, check=True).stdout
    return {r["Old"]["Path"]: r["New"]["Path"]
            for r in (json.loads(out).get("Replace") or [])}


def sbom_modules(path: str) -> set[str]:
    with open(path) as f:
        components = json.load(f).get("components", [])
    return {f"{c['name']}@{c.get('version', '')}"
            for c in components
            if str(c.get("purl", "")).startswith("pkg:golang/")}


def main() -> int:
    if len(sys.argv) not in (3, 5):
        print(__doc__.strip().splitlines()[2], file=sys.stderr)
        return 2

    sbom_path, package = sys.argv[1], sys.argv[2]
    goos, goarch = (sys.argv[3], sys.argv[4]) if len(sys.argv) == 5 else (None, None)

    from_source = go_list_modules(package, goos, goarch)
    from_binary = sbom_modules(sbom_path)
    replaced = replacements()

    def normalise(mods: set[str]) -> set[str]:
        out = set()
        for mod in mods:
            path, _, version = mod.rpartition("@")
            if path in ("stdlib", ""):
                continue
            out.add(f"{replaced.get(path, path)}@{version}")
        return out

    source_set = normalise(from_source)
    binary_set = normalise(from_binary)

    # The main module is the thing being described, not a dependency, and
    # the two sides stamp its version differently (the binary carries a
    # pseudo-version derived from the commit; go list reports none).
    main_module = subprocess.run(["go", "list", "-m"], capture_output=True,
                                 text=True, check=True).stdout.strip()
    source_set = {m for m in source_set if not m.startswith(main_module + "@")}
    binary_set = {m for m in binary_set if not m.startswith(main_module + "@")}

    only_binary = sorted(binary_set - source_set)
    only_source = sorted(source_set - binary_set)

    if not only_binary and not only_source:
        print(f"{sbom_path}: {len(binary_set)} modules, agreeing with "
              f'"go list -deps {package}"')
        return 0

    print(f"{sbom_path}: disagrees with the source of {package}", file=sys.stderr)
    for mod in only_binary:
        print(f"  in the binary only: {mod}", file=sys.stderr)
    for mod in only_source:
        print(f"  in the source only: {mod}", file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())
