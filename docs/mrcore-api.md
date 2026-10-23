<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->

# mrcore API: proposed trimming before a standalone release

Status: proposal only. Nothing in this document has been applied. The
protocol itself is specified in `docs/mr1-spec.md`; the transport
subpackage has its own review in `docs/transport-api.md`.

Markers: `[V]` verified by running something, `[I]` read from the source
but not run, `[U]` unknown.

## 1. Exported surface today

`go doc -all ./mrcore` `[V]` lists:

| Group | Symbols |
|---|---|
| Protocol operations | `Enrol`, `ChallengeStart`, `FinishFullPoint`, `FinishXOnly`, `ValidateChallengePoint`, `Wipe`, `SafeLabel` (added since this list was measured: makes text chosen by the other side safe to print) |
| Group abstraction | `Group` (interface of seven methods), `P256()` |
| Token | `Token`, `MachineInfo`, `Recipient`, `Recipient.TransportDeviceID`, `Token.MarshalJSON`, `Token.UnmarshalJSON` |
| Recovery wire | `Hello`, `ConfirmCodeChallenge`, `ConfirmCodeResponse`, `RecoverRequest`, `RecoverResponse`, `RecoverResult`, `WriteMessage`, `ReadMessage` |
| Pairing | `NewPairingSecret`, `DecodePairingSecret`, `PairProof`, `PairChallenge`, `PairRequest`, `PairResult` |

## 2. Who uses what

Counted by grepping for `mrcore.<Name>` under `cmd/`, `mobile/` and
`mrcore/transport/` `[V]`. Every symbol in the table above has at least
one use outside `mrcore` except the following:

- `mrcore.Group` has exactly one reference outside `mrcore`: the
  parameter type of a test helper, `recoverPForTest` in
  `cmd/unlocker/enrol_test.go` `[V]`. No production code names the type.
- Of the seven `Group` methods, production code outside `mrcore` calls
  only `RandomScalar`, `ScalarBaseMult` (`cmd/keyholder/keystore.go`),
  `ScalarMult` (`cmd/keyholder/unlock.go`) and `PointToX`
  (`cmd/unlocker/pair.go`, used purely to validate a public key) `[V]`.
  `Add`, `Negate` and `LiftX` are called only inside `mrcore` `[V]`
  (a grep for `.Add(` outside `mrcore` finds only `time.Time.Add` and
  `sync.WaitGroup.Add`). `mobile/mobile.go` uses `P256()` only to pass
  it on to `Enrol`, `ChallengeStart`, `FinishXOnly` and
  `ValidateChallengePoint` `[V]`.
- `MarshalJSON` and `UnmarshalJSON` are used implicitly through
  `encoding/json` and are part of the token contract, not candidates for
  removal.

## 3. Proposals

Ordered by value for a first standalone release. Each is a breaking
change for the callers named, which are all inside this repository.

### 3.1 Remove the `Group` parameter, keep one curve

`Enrol`, `ChallengeStart`, `FinishFullPoint`, `FinishXOnly` and
`ValidateChallengePoint` all take `g Group`. Only P-256 exists, and the
token format (`version` 1) and the HKDF `info` string pin P-256 anyway
(spec section 12.12). The interface is speculative generality: its own
doc comment mentions a hypothetical ristretto255 group that nobody has
written, and a second group would need a new token `version`, not just
a new `Group` value.

Proposal: unexport `Group` and `p256Group`, drop the parameter from the
five functions above, and add exactly the three helpers the key holder
needs, so that callers stop reaching through `P256()`:

    func GenerateKeyScalar() (s []byte, err error)
    func PublicPoint(s []byte) (S []byte, err error)
    func Respond(x, s []byte) (y []byte, err error) // validates x, then s.x

`Respond` folds `ValidateChallengePoint` into the one operation that
needs it, which removes the chance of a key holder forgetting the check
(spec section 12.2). A second helper for x-only responses is not needed:
that case is done by hardware and never reaches `mrcore`.

Cost: touches the 45 `mrcore.P256()` call sites `[V]`, most of them in
tests. Callers that currently hold a `Group` for testing can use the
helpers instead.

Alternative if a second curve is genuinely planned: keep `Group`, but
unexport `Add`, `Negate` and `LiftX` from it by splitting the interface
into a small exported one (what key holders need) and an unexported one
(what recovery needs). That keeps the extension point and shrinks what an
implementer must write.

### 3.2 Pairing symbols

`PairChallenge`, `PairRequest` and `PairResult` are exported because
`cmd/unlocker`, `cmd/keyholder` and `mobile` all construct them `[V]`;
they stay. The pairing helpers `NewPairingSecret`, `DecodePairingSecret`
and `PairProof` are also used by those packages and stay. If the
pairing step is split into its own module later, these six symbols
should go with it, since the MR-1 spec does not cover them
(spec section 9.2, last paragraph).

### 3.3 Give `Recipient` and `Token` constructors or validation

`Token` and `Recipient` are plain structs with exported fields. Nothing
validates a decoded `Recipient` (lengths of `kid`, `S`, `C`, `ct`,
`nonce`) until `Finish*` uses it `[I]`. For a library, either document
that callers get validation only at use time, or add
`func (r Recipient) Validate() error` and call it from
`Token.UnmarshalJSON`. Not needed for the current callers, which treat a
bad token as a failed recovery. A decision for the maintainer.

### 3.4 `Wipe`

`Wipe` is used 16 times outside `mrcore` `[V]` and is documented as best
effort. Keep it, but consider renaming to say so (`WipeBestEffort`) or
moving it to an `internal/` package: it is a generic helper with no
MR-1 content, and exporting it makes it part of the stability promise.
Low priority.

### 3.5 What to freeze

For a first release the stable surface should be what the spec
specifies: the five operations (`Enrol`, `ChallengeStart`,
`FinishFullPoint`, `FinishXOnly`, plus the key-holder side of 3.1), the
`Token` and `Recipient` JSON forms, and the six recovery messages with
`WriteMessage` and `ReadMessage`. Everything else in section 1 should be
either unexported or documented as unstable until the module's
extraction is decided.

## 4. Things that would break

- `mobile/` is built with gomobile for Android and calls into `mrcore`
  `[I]`; any signature change needs the Kotlin side rebuilt. This was
  not built or run for this document `[U]`.
- The test vectors (`testdata/mr1.json`) and the token golden test pin
  the wire and token bytes, not the Go API, so none of these proposals
  alters them. That is the property to preserve: a refactor of the Go
  surface must leave `go test ./mrcore/...` and the golden token test
  green with no edits to the expected bytes.
