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

A later deep re-review found and fixed additional cross-platform defects. This
paragraph and the following list are retained as the earlier review snapshot;
section 10 supersedes them with the final Windows-side findings and gates.

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

## 10. Windows deep re-review closure (2026-08-31)

This section supersedes the review results, hashes, timings, and residue claim
in section 9. The production-disabled boundary and all macOS qualification
limitations remain unchanged. The re-review refreshed both `origin/main` and
`origin/codex/shadow-ephemeral-poc`; neither remote changed while this work was
in progress.

### Reviewed commits and method

| Repository | Integrated `origin/main` | Deep-review code commit |
| --- | --- | --- |
| `v-local-key-provider` | `bd477b923a0085e737e115fa80590f23cffb4ea7` | `8124bb379cb6629c2d5d42fce78848b902847e53` |
| `v-local-cli` | `d6f5e24b32a7c049dad652aaea2d1fe89acfd5e4` | `8b769c26b92af626ba6b936c9ae1482394cb4a40` |

The review covered every function in the Shadow-touched packages and the
supporting startup, state, command, process, inventory, source, build-set,
Keychain, and supervisor boundaries. It also used whole-repository tests,
`vet`, staticcheck, vulnerability scanning, added-line residue scans, stress
runs, Linux race instrumentation, and Darwin no-cgo cross-builds. Only the
handoff commits from `codex/shadow-ephemeral-poc` were integrated onto the
current `origin/main`; no earlier Shadow implementation was used.

### Additional defects found and fixed

1. The shared decoder accepted contradictory states and incompletely bound
   frozen request/qualification/receipt fields. Golden vectors now cross-bind
   every frozen field, state-dependent qualification presence, timing totals,
   resource modes, and clone/supervisor digests in both repositories.
2. Build-set reads could accept hard links and source identity was not observed
   across the complete inventory interval. Reads now reject multi-link files,
   recheck path/descriptor identity, and verify source code identity, version,
   rewrite inputs, per-file metadata, and inventory before and after use.
3. Startup reconciliation silently ignored account-ID-shaped non-directories.
   Such entries now fail closed as unreadable state, with deterministic tests.
4. The CLI Keychain helper did not handle short output writes and cleared only
   the written prefix. It now completes short writes and clears the full secret
   buffer; tests exercise both properties.
5. Provider command dispatch trusted some already-decoded inner bindings. It
   now independently recomputes the options HMAC from the outer catalog key and
   exact account/database/scopes, checks request identity at the command
   boundary, and revalidates runner request, qualification, and receipt output.
6. Recovery accepted resource combinations that normal execution cannot
   produce. Recovery states now model only reachable prepared and launched
   resource sets; a process or supervisor requires the complete launch set.
7. A supervisor validation failure could close the inherited status descriptor
   without a status byte, allowing EOF to be mistaken for successful exec.
   Every reportable failure now writes an explicit failure byte; direct and
   end-to-end replacement tests cover the boundary.
8. Darwin gate setup failures could escape without structured JSON. They now
   emit the same secret-free failed result and failure exit status as later gate
   failures.
9. Closing a Windows Job Object does not synchronously prove all descendants
   have exited. The Windows qualification path now explicitly terminates the
   job and waits, with a bound, until `ActiveProcesses == 0` before plaintext
   cleanup. The descendant-cleanup test passed 20 consecutive runs.
10. Platform test seams had leaked into Darwin behavior and a 300 ms synthetic
    lease became invalid under race instrumentation. Seams are Linux-only;
    Darwin uses the native runtime and process-birth implementation. The lease
    is now 2 seconds while still exercising real expiry.
11. Deprecated Windows token acquisition and two unrelated baseline staticcheck
    findings were cleaned up. Full production-code staticcheck is now clean in
    both repositories on Windows and Darwin no-cgo.

### Exact final gates

All final Go commands used the downloaded official `go1.26.6` toolchain and a
repository-local `GOCACHE`. The Provider full Windows suite intentionally used
the system temporary directory because its native DACL and named-pipe tests
depend on Windows temp semantics.

```powershell
$env:GOTOOLCHAIN='go1.26.6'
$env:GOCACHE=(Resolve-Path .tmp-go-cache).Path
go test -count=1 ./...
go vet ./...
go mod verify
npm test
```

Native Windows results:

- CLI `go test -count=1 ./...`: PASS (`internal/app` 20.829s,
  `internal/provider` 7.267s, `internal/wxgfqual` 6.781s, and
  `internal/store` 7.242s).
- Provider `go test -count=1 ./...`: PASS (root 12.421s,
  `internal/acquisition` 11.783s, `internal/command` 2.951s, and
  `internal/shadow` 2.412s).
- `go vet ./...`: PASS in both repositories (exit 0, no diagnostics).
- `go mod verify`: `all modules verified` in both repositories.
- CLI npm: 12/12 PASS in 950.2269ms; Provider npm: 13/13 PASS in
  1172.3918ms.
- CLI Shadow/state stress (`-shuffle=on -count=20`): PASS; synthetic Owner and
  startup reconciliation (`-count=20`): PASS in 5.481s.
- Provider command/Shadow/contract stress (`-shuffle=on -count=20`): PASS.
- Windows descendant cleanup
  `go test -count=20 -run '^TestWindowsProviderJobKillsDescendantOnClose$' ./internal/wxgfqual`:
  PASS in 41.561s.
- Native Windows `go test -race -count=1 ./...`: **not executed**. It exits 1
  with `go: -race requires cgo; enable cgo by setting CGO_ENABLED=1` because
  this host has no usable Windows cgo compiler. This is not a pass.

The following Unix gates were executed under WSL Ubuntu with Go 1.26.6:

```powershell
wsl.exe -d Ubuntu --cd /mnt/d/Projects/zanescope/v-local-cli env GOTOOLCHAIN=go1.26.6 go test -count=1 ./...
wsl.exe -d Ubuntu --cd /mnt/d/Projects/zanescope/v-local-cli env GOTOOLCHAIN=go1.26.6 go test -race -count=1 ./...
wsl.exe -d Ubuntu --cd /mnt/d/Projects/zanescope/v-local-key-provider env GOTOOLCHAIN=go1.26.6 go test -count=1 ./...
wsl.exe -d Ubuntu --cd /mnt/d/Projects/zanescope/v-local-key-provider env GOTOOLCHAIN=go1.26.6 go test -race -count=1 ./...
```

Both complete non-race suites and both complete race suites passed. The final
CLI race run included `internal/app` 108.872s, `internal/store` 21.990s, and
`internal/wxgfqual` 20.829s. The final Provider race run included root 12.236s,
`internal/acquisition` 23.138s, and `internal/supervisor` 10.293s. `go vet
./...` also passed in both WSL checkouts. These are Linux/Unix results, not
Windows race or macOS runtime results.

Darwin compile-only gates passed for both `amd64` and `arm64` in both
repositories:

```powershell
$env:GOTOOLCHAIN='go1.26.6'
$env:GOOS='darwin'
$env:GOARCH='amd64' # repeated with arm64
$env:CGO_ENABLED='0'
go test -count=1 -run '^$' -exec='cmd /c echo' ./...
go vet ./...
```

No Darwin test binary ran and Darwin cgo files were not compiled. These results
prove only the no-cgo build surface. Full test-aware staticcheck
`-checks='all,-ST*,-U1000,-SA1012'` and production-code staticcheck
`-tests=false -checks='all,-ST*,-U1000'` passed on native Windows and Darwin
arm64 no-cgo for both repositories. The production-code check was rerun after
the Windows Job Object fix and produced no diagnostics.

`govulncheck ./...` initially reported five reachable standard-library findings
with the host's Go 1.26.5 toolchain (`GO-2026-6218`, `GO-2026-6090`,
`GO-2026-6088`, `GO-2026-5972`, and `GO-2026-5026`) in the CLI; the Provider had
none reachable. Both repositories report `No vulnerabilities found.` with Go
1.26.6, which is the toolchain used for the final gates. This does not assert
that future vulnerability databases will remain unchanged.

### Frozen shared bytes

Exact byte comparison passed for every shared contract/build-set artifact:

| Shared artifact | SHA-256 |
| --- | --- |
| `internal/shadowcontract/contract.go` | `d242b63081c631d73180a8fd42f2cbedd155b0d6b06f9fa14a056a8cb2b141ab` |
| `internal/shadowcontract/contract_test.go` | `604c4f2885201f179a681072a46910ee717ad3f4106181cea43742423add2bb0` |
| `internal/shadowbuildset/buildset.go` | `f6c66b5fcd2421d7d2fa36fc3c7affc6a1ccecffc186beee7faba22f1c62fdbc` |
| `internal/shadowbuildset/buildset_test.go` | `188257c31c86be454eb42fbcbb2c4e24b49bb8e920b30a214dba221601357ab8` |
| `internal/shadowbuildset/identity_unix.go` | `e610b8cd0daf76e8627417389b91cad6dbe16e3693695181827922ba8c41cef6` |
| `internal/shadowbuildset/identity_windows.go` | `3894c9225072a799cc1d36755e28b390210e104fdf851eca0ac64129e7049d42` |
| `internal/shadowclock/clock.go` | `27dfe6fa1e0d571a185464cff18cdd9a6450892d69c093015abe2672f791c8b5` |
| `internal/shadowaccount/account.go` | `5d9a98fe19d557a14cd9c97fdfaf4e18d13d219aeb3488fae2c5d211f458eea2` |
| `testdata/shadow-contract-v1.json` | `076179a56a4986e281225f20e338afcccb0a09c6fa192dda2999c53627da8fd2` |
| `testdata/shadow-build-set-v1.json` | `6b63d4f976c936d986d9dc23d2992f7e83a70e3bbbb10c178bbfc20aacc8d698` |

The inventory and source wrappers intentionally differ by module import,
package comment/helper, and package-specific tests; they received equivalent
hardening but are not represented as byte-identical artifacts.

### Route and residue closure

- `shadowproduction.NewDisabledCoordinator` remains disabled for qualification
  and execution, Provider production execute still returns
  `shadow_production_route_disabled`, and the CLI production Owner rejects a
  disabled qualification. No production assembler or route was added.
- macOS interfaces and build tags are unchanged. No capture, LaunchServices,
  Keychain trust, signing, TCC/FDA, SIP, or real-machine behavior was invented.
- `git diff --check`, full production staticcheck, placeholder/TODO/debug/local
  path scans, and untracked-file inspection are clean after removing test and
  analysis caches. The committed diff contains no temporary artifacts.

### Findings returned to macOS

These items cannot be closed faithfully from Windows or Linux. They are
explicit Mac-side review, fault-injection, or real-machine qualification work:

1. Validate Security.framework `/dev/fd/N` static identity and running-guest
   CDHash semantics; identity evidence is not signature-validity evidence.
2. Exercise real Keychain add/get/delete, locked/UI/accessibility behavior,
   duplicate and crash cleanup, and secret-memory lifetime.
3. Validate `proc_pid_rusage` process-birth identity and PID reuse with Darwin
   cgo and native fault injection.
4. Qualify APFS `clonefile`, file/directory fsync and rename durability,
   LaunchServices, codesign/DER entitlements, real resigning/capture, TCC/FDA,
   minimal environment, SIP-off acquisition, and SIP restoration.
5. Decide how to close the residual path race between the final target digest
   and `syscall.Exec` (for example, fd-based exec or immutable staging), then
   exercise the selected design adversarially on macOS.
6. Prove that supervisor-hang fallback removes the exact native process tree
   and artifacts; current exact-absence checks fail closed but are not a native
   process-tree proof.
7. Separate a genuinely missing plist entitlement key from codesign/plist
   inspection failure and confirm the intended enumeration policy fail-closed.
8. Source traversal is per-file hardened but not one atomic, ancestor-bound,
   fd-relative snapshot across a multi-component tree. Review the Mac threat
   model and add native ancestor/replacement fault injection.
9. Inject a coordinator crash after the supervisor starts but before durable
   PID binding. EOF/lease handling is expected to clean up, but that expectation
   needs native evidence.
10. Exercise crash cleanup when a staged inode was never made durable; ensure
    recovery cannot bind a replacement at the same randomized attempt path.
11. Transformation discovery currently skips candidates whose
    signed-executable inspection fails. Final strict verification fails later,
    but macOS must decide whether enumeration itself must abort immediately.

Consequently, macOS runtime/cgo, signing, Keychain, capture, LaunchServices,
TCC/FDA, SIP-off, native crash/fault behavior, and the real T+120 workflow remain
unverified. Neither Windows runtime success, WSL race success, nor Darwin
compile-only success may be reported as a macOS machine qualification result.
