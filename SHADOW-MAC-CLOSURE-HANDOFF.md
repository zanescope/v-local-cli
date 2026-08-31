# Shadow macOS closure handoff

> Date: 2026-08-31 (Asia/Shanghai)
>
> This document is intentionally identical in `v-local-key-provider` and
> `v-local-cli`. It records the local implementation commits and the exact
> boundary handed to a Windows-side continuation. It is not a machine
> qualification receipt and must not be used to enable the production route.

## 1. Frozen starting point

| Repository | Remote | Opening `origin/main` / merge-base | Implementation commit |
| --- | --- | --- | --- |
| `v-local-key-provider` | `git@github.com:zanescope/v-local-key-provider.git` | `bd477b923a0085e737e115fa80590f23cffb4ea7` | `aa9657c53aa10a4236a454d3ed9094f5ea83cdb2` |
| `v-local-cli` | `git@github.com:zanescope/v-local-cli.git` | `8dec470fef4901c599b5e2f87f4d4cdaa49b6ec1` | `6949dfcd2e805d6431240eea0584040f10e387bc` |

Both implementation commits are on `codex/shadow-ephemeral-poc`. That branch is
the handoff vehicle; verify that each remote branch contains the corresponding
implementation commit above and this document before continuing. The CLI
checkout retains the user's pre-existing untracked `.DS_Store`; it is not part
of any commit.

The opening baseline was fetched before implementation and baseline tests were
run before source changes. Do not replace these checkouts, restore an older
Shadow patch, or treat a later `origin/main` as the recorded baseline.

### Post-closure upstream check

Immediately before branch publication, `git fetch origin main` produced this
state:

| Repository | Current `origin/main` | `origin/main...HEAD` | Integration status |
| --- | --- | --- | --- |
| `v-local-key-provider` | `bd477b923a0085e737e115fa80590f23cffb4ea7` | `0 2` | unchanged from the frozen baseline |
| `v-local-cli` | `d6f5e24b32a7c049dad652aaea2d1fe89acfd5e4` | `2 2` | diverged after the closure |

The CLI upstream additions are `ff02a7d` and `d6f5e24`. A read-only
three-way `git merge-tree` check found common edits in
`internal/app/app.go` and `internal/app/skill_contract_test.go` but no textual
conflict. Semantic integration is **not verified**: this handoff branch was not
rebased or merged, and Windows must integrate it onto a fresh current baseline
and rerun all native and contract gates.

## 2. Scope that is complete

The committed scope is a fail-closed foundation:

- byte-identical shared Shadow contract and build-set models;
- fixed T0 monotonic deadlines and stage cutoffs;
- exclusive journal/lock, recovery record, one finalizer, detached bounded
  cleanup, and secret-free receipt;
- synthetic coordinator and executable Provider-to-CLI vertical slice;
- exact workspace/container/LaunchServices abstractions and direct cleanup
  qualification;
- deterministic transformation manifest, deepest-first signing, and unchanged
  `codesign --verify --deep --strict`;
- inherited-FD supervisor with launch-before-release gating, absolute lease,
  exact process-group cleanup, and OS-observed PID birth identity on Darwin;
- read-only production source/build qualification and CoW/transformation-only
  qualification commands;
- CLI approval, independent verifier interface, candidate validation,
  generation transaction, durable non-secret state, and bounded Keychain helper;
- Windows build tags/stubs and compile-only portability checks.

Two review defects were fixed before the implementation commits:

1. supervisor process start facts now use Darwin
   `proc_pid_rusage(...).ri_proc_start_abstime`, converted with the Mach
   timebase, instead of a sampled coordinator clock;
2. CoW/transformation qualification now rejects a nil lock release and cannot
   return a `qualified` summary when lock release fails.

The Provider npm installer test also canonicalizes the macOS temporary root
before testing symlink-ancestor rejection. Production ancestor validation was
not relaxed.

No known TODO/FIXME/unimplemented/panic placeholder, local absolute path, gate
snapshot path, or debug logger exists in the committed Shadow diff. All
destructive primitives are exact-binding operations; none discovers a target by
process name, app name, bundle-ID prefix, or directory-wide best match.

## 3. Deliberately disabled or absent

This is a scope-complete foundation, not a production-complete route.

- Provider production `execute` returns
  `shadow_production_route_disabled`.
- `shadowproduction.NewDisabledCoordinator` keeps both execution routes off.
  Enabling it requires a separate code change after ordered machine gates.
- No concrete real `CaptureRuntime` exists.
- No concrete production LaunchServices adapter is wired into
  `RuntimeDependencies`.
- `SystemProcesses` exists on Darwin+cgo and re-observes exact PID/birth
  pairs, but the production coordinator is not assembled.
- CLI has no public real Shadow Owner command and no concrete macOS
  `shadowverify.Probe`.
- CLI startup does not yet invoke generation reconciliation.
- The Keychain backend/helper is implemented and unit-tested, but no real
  Keychain write was run in this closure.
- No current production build set was frozen after these commits. The older
  root evidence directory
  `.evidence/shadow-buildset-20260830-qualification-01` is stale and must not
  be used for acceptance.
- Windows packages were compiled from macOS; they were not executed on a
  Windows machine.
- No real target resign, launch/capture, real container mutation, TCC/FDA
  action, SIP change, or SIP-off fallback was performed.

Do not “finish” the route by flipping `ProductionRouteEnabled`, weakening
qualification matching, relaxing codesign verification, or substituting
synthetic results for machine evidence.

## 4. Current automated evidence

Status: **自动化门禁通过** for the two implementation commits above.

The following passed on macOS arm64 with Go 1.25.14 and
`GOTOOLCHAIN=local`:

### Provider

```sh
GOCACHE=/private/tmp/v-local-go-cache /Users/zhengshijie/Documents/ChatGPT/zanescope/.tools/go1.25.14.nosync/bin/go test -count=1 ./...
GOCACHE=/private/tmp/v-local-go-cache /Users/zhengshijie/Documents/ChatGPT/zanescope/.tools/go1.25.14.nosync/bin/go test -race -count=1 ./...
GOCACHE=/private/tmp/v-local-go-cache /Users/zhengshijie/Documents/ChatGPT/zanescope/.tools/go1.25.14.nosync/bin/go vet ./...
/Users/zhengshijie/Documents/ChatGPT/zanescope/.tools/go1.25.14.nosync/bin/go mod verify
npm --prefix npm test
```

Results: all Go packages passed; npm passed 13/13. Race emitted non-fatal
`malformed LC_DYSYMTAB` linker warnings and exited 0.

### CLI

```sh
GOCACHE=/private/tmp/v-local-cli-go-cache /Users/zhengshijie/Documents/ChatGPT/zanescope/.tools/go1.25.14.nosync/bin/go test -count=1 ./...
GOCACHE=/private/tmp/v-local-cli-go-cache /Users/zhengshijie/Documents/ChatGPT/zanescope/.tools/go1.25.14.nosync/bin/go test -race -count=1 ./...
GOCACHE=/private/tmp/v-local-cli-go-cache /Users/zhengshijie/Documents/ChatGPT/zanescope/.tools/go1.25.14.nosync/bin/go vet ./...
/Users/zhengshijie/Documents/ChatGPT/zanescope/.tools/go1.25.14.nosync/bin/go mod verify
npm --prefix npm test
```

Results: all Go packages passed; npm passed 12/12. Race emitted the same
non-fatal linker warning and exited 0.

### Cross-platform compile gates

For both repositories, all of the following passed:

```sh
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -run '^$' -exec /usr/bin/true ./...
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...
GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build ./...
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build ./...
```

The Windows `go test` command is compile-only because `/usr/bin/true`
replaces execution. It is not Windows runtime evidence. Darwin production
process-birth observation and the Keychain store require cgo; no-cgo builds
remain fail-closed.

### Contract identity

The two repositories are byte-identical for both files:

- `testdata/shadow-contract-v1.json`:
  `076179a56a4986e281225f20e338afcccb0a09c6fa192dda2999c53627da8fd2`
- `testdata/shadow-build-set-v1.json`:
  `6b63d4f976c936d986d9dc23d2992f7e83a70e3bbbb10c178bbfc20aacc8d698`

### Synthetic executable vertical slice

The current Provider binary was injected into:

```sh
V_LOCAL_SHADOW_PROVIDER_TEST_BINARY=/absolute/path/to/v-local-key-provider \
  go test -count=1 \
  -run '^TestExecutableSyntheticVerticalSliceUsesRealProviderDaemon$' \
  ./internal/provider
```

It passed in 0.78 seconds of tool wall time (Go package result 0.69 seconds).
The test requires `status=ready` in under 10 seconds and proves:

- real Provider executable/daemon framing was used;
- CLI approval was consumed;
- Provider cleanup receipt was independently checked;
- candidate validation completed before publication;
- pending generation was removed;
- exactly one minimal credential was stored in the memory Keychain backend;
- transient diagnostics/receipt data was not stored.

This is synthetic evidence only. It is not the real T+120 acceptance.

## 5. Non-frozen candidate hashes

These hashes identify the final local build run from source matching the
implementation commits. They are useful only for diagnosing handoff drift.
They are not a production build-set manifest:

| Artifact | SHA-256 |
| --- | --- |
| Provider Darwin arm64 cgo | `e6aa2fb6290bea75f5f5712d6198d8598c69668c953825da573cca619e0144b7` |
| Supervisor Darwin arm64 cgo | `a4728646c0bebe476422f4c63c88986ae8a10524a02aa5258b6981aa66de181d` |
| Provider Darwin amd64 no-cgo | `2c8a1dc7cc56b29d656f6e0b30c07e9f4523a069d46a0a591d8c9c27651f6962` |
| Provider Windows amd64 | `b44606bc32fa5482cec3578a96ec762c8d8e08acadc4f1391a1993857158f808` |
| CLI Darwin arm64 cgo | `16290802fc89cd19b8bb6144a90ea2bf9130498233e6bf848c0c830439eaa9e3` |
| CLI Darwin amd64 no-cgo | `caf3b2ee097346adbe4e04fc55c075e793b34b3cf74ff51357d845b91e185fe5` |
| CLI Windows amd64 | `5459981c32730609c28b2af3e373c6bd722b001f1c4c02af14b9ba802987395a` |

## 6. Windows-side continuation boundary

Windows may safely continue the cross-platform composition without guessing
macOS behavior:

1. run both repositories' full native Windows tests, race where supported,
   vet, npm tests, and contract byte comparison;
2. wire CLI AttemptOwner into an internal synthetic-only command and add
   end-to-end tests for approval, deadline, verifier, candidate validator,
   publisher, and startup reconciliation;
3. wire startup generation reconciliation so a pending generation is always
   exact-delete-and-verify before the account can be reported ready;
4. add crash/race tests around pre-ready and post-ready publication boundaries;
5. preserve all Darwin interfaces and build tags; keep production execution
   disabled;
6. return a clean commit set plus native Windows command output.

Windows must not invent implementations for real macOS capture,
LaunchServices, process inspection, Keychain trust behavior, signing, or source
qualification. Those remain Mac-return work.

## 7. Mac-return sequence

After Windows changes are reviewed and merged/cherry-picked into fresh branches:

1. re-run both complete automated gates and native contract comparison;
2. implement and test concrete macOS LaunchServices, CaptureRuntime, and CLI
   independent Probe;
3. assemble the production coordinator while keeping execution disabled;
4. freeze one new build set from exact commits and record all artifact digests;
5. request separate qualification windows, in order, for:
   - read-only source/build qualification;
   - real target transformation-only clone/resign/destroy;
   - launch/capture;
   - disposable container mutation and cleanup;
   - TCC/FDA if required;
   - complete Shadow flow and real Keychain generation;
   - SIP-off acquisition;
   - SIP restoration and final residue audit;
6. stop immediately on any anomaly and prove residue before the next window.

Do not combine these approvals.

## 8. Acceptance status

- **自动化门禁通过**: yes, for the implementation commits in section 1.
- **真机子门禁通过**: 不确定; no current real-machine sub-gate was executed.
- **120 秒端到端闭环通过**: 不确定; only the synthetic executable loop passed.
- **Shadow production route**: disabled.
- **SIP-off fallback**: unverified and untouched.
- **Goal completion**: no. The original goal must remain incomplete until every
  ordered Shadow and SIP-off machine condition passes on one frozen build set.

## 9. Windows continuation addendum (2026-08-31)

This section supersedes the Windows-side status statements above. The earlier
sections remain as the immutable incoming handoff record. The continuation was
created from the latest `origin/main` and integrated only the implementation
commit identified in section 1 plus its two immediately following handoff-doc
commits; no older Shadow implementation was imported.

### Completed Windows and cross-platform work

The handoff-touched Shadow packages, including Darwin-only/no-cgo files, were
reviewed at function granularity. The continuation now provides:

- an internal hidden `__shadow-synthetic-owner --confirm` entry point that
  composes the real Owner, approval, verifier, candidate validator, publisher,
  and reconciliation state machine entirely with synthetic/in-memory adapters;
- startup reconciliation for every discovered account, before normal command
  execution, with a shared deadline, deterministic ordering, duplicate/empty
  account rejection, exact pending-generation cleanup, and lazy Darwin frozen
  build-set/Keychain construction;
- crash tests on both sides of the ready marker and concurrent
  publish/reconcile tests;
- host-independent wire-path validation and inventory conversion, including
  rejection of backslashes, drive/ADS colons, absolute paths, traversal, and
  duplicate/non-canonical scopes;
- post-read file identity checks for build artifacts, approval state,
  publication state, and the Provider journal, including device/inode, owner,
  mode, link count, size, and mtime checks where available;
- nanosecond clock overflow/range validation shared byte-for-byte by both
  repositories;
- Provider frame short-write handling, thread-safe locked-memory bookkeeping,
  cancellation-aware transformation, exact plist rewrite verification,
  sanitized discovery subprocess environments, and pre/post-transform frozen
  build-set revalidation;
- a second target/canonical-path/digest validation after supervisor release and
  immediately before exec, plus Unix target-drift fault injection;
- retention of canonical paths produced by `Frame.validateInit` (the previous
  value receiver silently discarded them);
- rejection by the CLI of every Provider acquisition payload field during
  read-only qualification, not only the originally checked credential subset.

Production execution remains disabled. The continuation does not assemble or
enable a production route and does not implement or infer macOS capture,
LaunchServices, Keychain trust, signing, TCC/FDA, or real-machine qualification.

### Reproducible Windows gates

Run the common non-test gates from each repository:

```powershell
New-Item -ItemType Directory -Force .tmp-go-cache | Out-Null
$env:GOCACHE=(Resolve-Path .tmp-go-cache).Path
go vet ./...
go mod verify
npm test
```

The final full CLI test used a workspace-local Go temporary directory:

```powershell
$env:GOCACHE=(Resolve-Path .tmp-go-cache).Path
$env:GOTMPDIR=(Resolve-Path .tmp-go-cache).Path
go test -count=1 ./...
```

The final full Provider test deliberately used the native Windows system
temporary directory because its DACL and named-pipe tests exercise native temp
semantics:

```powershell
$env:GOCACHE=(Resolve-Path .tmp-go-cache).Path
Remove-Item Env:GOTMPDIR -ErrorAction SilentlyContinue
go test -count=1 ./...
```

Results on native Windows:

- CLI `go test -count=1 ./...`: PASS (including `internal/app` 17.657s and
  `internal/provider` 6.039s);
- Provider `go test -count=1 ./...`: PASS (including the root package 7.384s
  and `internal/acquisition` 7.860s);
- `go vet ./...`: PASS in both repositories;
- `go mod verify`: `all modules verified` in both repositories;
- CLI npm: 12/12 tests PASS (587.7873ms);
- Provider npm: 13/13 tests PASS (792.3302ms).

The Windows host reports `CGO_ENABLED=0` and has no supported C compiler, so
`go test -race` exits with `-race requires cgo`. This is an explicitly
unexecuted gate, not a pass. The normal suite includes the concurrent
publish/reconcile and locked-memory stress tests.

### Contract byte identity

PowerShell `SequenceEqual[byte]` comparison passed for every shared contract
artifact. SHA-256 values are:

| Shared artifact | SHA-256 |
| --- | --- |
| `internal/shadowcontract/contract.go` | `a1fc8158246b52852072863483585c44855c98dcf66b3b275ed4d5770a4f62de` |
| `internal/shadowbuildset/buildset.go` | `5b34694ab8278fbeda67a777a1f4d471d7b73bbd03101ba638147f03d4273558` |
| `internal/shadowclock/clock.go` | `27dfe6fa1e0d571a185464cff18cdd9a6450892d69c093015abe2672f791c8b5` |
| `internal/shadowaccount/account.go` | `5d9a98fe19d557a14cd9c97fdfaf4e18d13d219aeb3488fae2c5d211f458eea2` |
| `testdata/shadow-contract-v1.json` | `076179a56a4986e281225f20e338afcccb0a09c6fa192dda2999c53627da8fd2` |
| `testdata/shadow-build-set-v1.json` | `6b63d4f976c936d986d9dc23d2992f7e83a70e3bbbb10c178bbfc20aacc8d698` |

### Darwin compile-only checks

Both repositories passed the following for `amd64` and `arm64` with
`CGO_ENABLED=0`:

```powershell
$env:GOOS='darwin'
$env:GOARCH='amd64' # repeat with arm64
$env:CGO_ENABLED='0'
go test -count=1 -exec='cmd /c echo' ./...
go vet ./...
```

The test command compiles Darwin test binaries and delegates attempted
execution to an echo wrapper. No Darwin test binary ran. Darwin cgo files were
read and statically reviewed but cannot be compiled or executed on this host.

Formatting, `git diff --check`, and placeholder/TODO residue scans are clean.
Staticcheck is clean for the CLI `./internal/shadow...` packages and for the
Provider `./internal/command ./internal/shadow...` packages on native Windows
and Darwin arm64 no-cgo. Including the complete CLI entry packages also reports
`internal/app/app_test.go:1267` (`SA4006`) from the initial 2026-08-17 commit and
Darwin no-cgo reports the Windows release-signing injection variable as `U1000`;
both are unchanged from `origin/main` and outside this handoff diff.

### Mac-return findings and unverified boundaries

No statically certain defect remains in the reviewed Darwin cgo adapters, but
the following require Mac-side design review, fault injection, or qualification:

1. Verify Security.framework static-code identity via `/dev/fd/N` and running
   guest CDHash behavior. The current code-hash check is identity evidence, not
   proof of signature validity and not a replacement for the signing gate.
2. Exercise generic-password Keychain add/get/delete, locked-Keychain and UI
   behavior, accessibility attributes, duplicate/crash cleanup, and secret
   memory lifetime on a real Mac.
3. Validate `proc_pid_rusage` birth identity and PID-reuse behavior with native
   cgo tests.
4. Qualify APFS `clonefile`, directory/file fsync and rename durability,
   LaunchServices registration, codesign/DER entitlements, real resigning,
   capture, TCC/FDA, and minimal launch environment behavior.
5. The supervisor now revalidates after release, but a residual path race exists
   between its final digest and `syscall.Exec`. Do not claim closure without a
   Mac-side decision on an fd-based exec/immutable staging design and native
   adversarial testing.
6. If a supervisor hangs, client fallback termination does not itself prove
   cleanup of a separately grouped child. Exact-absence checks fail closed and
   retain recovery state, but Mac fault injection must prove process-tree and
   artifact residue behavior.
7. Discovery currently treats a missing entitlement key and a `codesign`/plist
   inspection failure as the same negative result. Mac qualification must prove
   the intended fail-closed policy before entitlement stripping is trusted.

Accordingly, macOS runtime, macOS cgo behavior, signing, Keychain semantics,
capture, LaunchServices, TCC/FDA, SIP-off acquisition, and the real T+120 flow
remain unverified. They must not be inferred from the Windows or compile-only
results above.
