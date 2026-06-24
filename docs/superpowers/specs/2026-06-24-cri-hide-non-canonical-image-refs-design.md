# Hide non-canonical image references from CRI

Issue: [#8848](https://github.com/containerd/containerd/issues/8848) Supersedes
the approach in PR
[#13077](https://github.com/containerd/containerd/pull/13077).

## Problem

Tagging an image with a "short" reference that omits a registry domain —
`ctr -n k8s.io images tag docker.io/library/busybox:1.36 busybox:fixed` — makes
the CRI image service display a reference that nothing can act on.
`crictl images` shows `docker.io/library/busybox:fixed`, but `crictl inspecti`,
`crictl rmi`, and `kubelet` pulls against that name all return "not found". The
same mechanism produces phantom duplicate rows when a short tag and a full tag
point at one digest.

## Root cause

The CRI image store keys its `refCache` by the **raw** containerd image name
(`Store.update` stores `ref` verbatim; `getImage` sets
`References: []string{i.Name}`). But `util.ParseImageReferences` reports the
**normalized** form, `parsed.String()`, as the RepoTag.

For a short tag those two disagree:

- stored / `refCache` key: `busybox:fixed`
- displayed RepoTag: `docker.io/library/busybox:fixed`

Every CRI operation resolves through `LocalResolve`, which normalizes its input
(`ParseDockerRef(req).String()`) before the `refCache` lookup. So a lookup of
the displayed name searches for `docker.io/library/busybox:fixed` while the
cache holds `busybox:fixed`. The reference is displayed but unresolvable.

A reference is therefore operable through CRI **iff its raw stored name already
equals its normalized form** — i.e. iff it is canonical. For the tag-only and
digest-only names that pull and tag flows actually produce, this is exact:
`ParseAnyReference(ref).String()` round-trips back to `ref` through
`ParseDockerRef` precisely when `ref` is canonical.

## Decision

Surface a reference from the CRI image service **only when it is canonical**,
detected with `reference.ParseNamed` (which returns `ErrNameNotCanonical` for
any non-canonical input). This filter _is_ the operability predicate, not a
heuristic that approximates it.

This subsumes the "explicit registry" framing. It correctly hides:

- `busybox:fixed`, `library/busybox:fixed` — no registry at all.
- `docker.io/busybox:1.36` — names a registry but omits the `library/`
  namespace; displayed as `docker.io/library/busybox:1.36`, unresolvable.
- `index.docker.io/library/busybox:1.36` — legacy domain rewritten to
  `docker.io` by `splitDockerDomain`; displayed as
  `docker.io/library/busybox:1.36`, unresolvable.

And keeps everything operable: `docker.io/library/busybox:1.36`,
`registry-1.docker.io/library/busybox:1.36`, `gcr.io/library/busybox:1.2`,
`localhost:5000/foo:bar`, canonical digest references.

### Why not the alternatives

- **PR #13077's `Domain()` + `HasPrefix(ref, domain+"/")` check.** Keeps
  `docker.io/busybox:1.36` visible-but-broken, and its raw-string handling is
  the fragility the PR reviewer flagged.
- **Reviewer's suggestion to sniff the raw first path component for a
  registry.** Would make `index.docker.io/...` and `docker.io/busybox:1.36`
  visible — both non-operable — reintroducing the bug for legacy-domain and
  namespace-less tags.
- **Filtering at store ingestion / event subscription.** More invasive, changes
  `Resolve`/`refCache` semantics, and the non-operable names can only enter via
  `ctr tag` (CRI's own `PullImage` normalizes before storing). No user-visible
  gain over read-surface filtering, which matches the "only surface" framing.

## Changes

All changes are on the read-surface path; the store stays a faithful mirror of
containerd, and an image remains resolvable by ID internally.

1. **`internal/cri/util/references.go`** — replace the `ParseAnyReference` body
   of `ParseImageReferences` with `reference.ParseNamed`; bucket the result by
   `reference.Canonical` / `reference.Tagged` as today. Non-canonical refs are
   skipped. Document the operability rationale.

2. **`internal/cri/server/images/image_status.go`** — `toCRIImage` returns `nil`
   when an image yields no qualified references (no RepoTags and no
   RepoDigests). `ImageStatus` returns an empty `ImageStatusResponse` in that
   case.

3. **`internal/cri/server/images/image_list.go`** — `ListImages` skips images
   for which `toCRIImage` returns `nil`.

`util.ParseImageReferences` has one other caller, `server/container_status.go`,
which builds a running container's RepoTags/RepoDigests. Applying the same
filter there is intentional: a CRI-launched container's image is always
canonical, so it is a no-op in practice, and the two surfaces agree on "which
references CRI acknowledges."

## Testing

- **Unit — `internal/cri/util/references_test.go`.** Add cases asserting the
  hidden set (`busybox:fixed`, `library/docker.io`, `library/docker.io:v1`,
  `busybox:docker.io`, `docker.io/busybox:1.36`, `index.docker.io/library/...`)
  and the kept set (`gcr.io/library/busybox:1.2`, a canonical digest,
  `registry-1.docker.io/library/busybox:1.36`).

- **Integration — `integration/images_visibility_test.go`** (new,
  `TestImageTagWithoutRegistryNotVisibleInCRI`). Short tag hidden from
  `ImageStatus` and `ListImages`; full tag visible with correct RepoTags. Per
  the PR review, the two hidden-tag assertions use `Consistently` (the CRI image
  store updates asynchronously, so a point-in-time check could miss a later
  exposure).

- **Regression — must keep passing:**
  - `FOCUS=TestImageTagWithoutRegistryNotVisibleInCRI make cri-integration`
  - `FOCUS=TestContainerdImage make cri-integration` — its image is
    `ghcr.io/containerd/busybox:1.36` (canonical), so it stays visible and its
    `RepoTags == [testImage]` assertion holds.

## Out of scope

- `ctr tag` validation or rewriting (a client-side concern, separate from CRI
  surfacing).
- Removing the `refCache` / second source of truth (noted as future work in PR
  #11920).
- Resolving a hidden image by its image ID still works; that is internal and not
  user-facing through `crictl images`.
- A name carrying both a tag and a digest is an unusual containerd image-name
  form that pull and tag flows do not produce; its handling is unchanged from
  today and not specially addressed.
