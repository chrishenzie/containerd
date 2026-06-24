# Hide non-canonical image references from CRI — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Hide image references that are not canonical (and therefore not
resolvable through CRI) from the CRI image service's `ImageStatus`,
`ListImages`, and `ContainerStatus` surfaces.

**Architecture:** Filter at the read-surface path. `util.ParseImageReferences`
keeps a reference only when `reference.ParseNamed` accepts it as canonical —
provably the same set CRI can resolve, because the store keys images by their
raw name but displays the normalized form. `toCRIImage` returns `nil` when an
image has no qualified references; `ListImages` skips those and `ImageStatus`
returns an empty response.

**Tech Stack:** Go, `github.com/distribution/reference`, CRI (`k8s.io/cri-api`),
containerd integration test harness.

## Global Constraints

- Design doc:
  `docs/superpowers/specs/2026-06-24-cri-hide-non-canonical-image-refs-design.md`.
- Module: `github.com/containerd/containerd/v2`, Go 1.26.3.
- Apache license header required on every new `.go` file (copy from any sibling
  file in the same directory).
- Commit message format: title ≤50 chars, no `prefix:`, blank line, body wrapped
  at 72 cols, with `Signed-off-by` and `Assisted-by: Claude Code` trailers. Use
  `git commit -s`.
- Run `git diff --check` before every commit.
- These two integration tests MUST pass unchanged in intent:
  - `FOCUS=TestImageTagWithoutRegistryNotVisibleInCRI make cri-integration`
  - `FOCUS=TestContainerdImage make cri-integration`
- Integration tests require sudo; the user runs them on request.

---

### Task 1: Canonical-only filter in `ParseImageReferences`

**Files:**

- Modify: `internal/cri/util/references.go`
- Test: `internal/cri/util/references_test.go`

**Interfaces:**

- Consumes: `reference.ParseNamed(string) (reference.Named, error)`,
  `reference.Canonical`, `reference.Tagged` from
  `github.com/distribution/reference`.
- Produces:
  `util.ParseImageReferences(refs []string) (tags []string, digests []string)` —
  unchanged signature; now returns only canonical references.

- [ ] **Step 1: Replace the test body of `TestParseImageReferences`**

In `internal/cri/util/references_test.go`, replace the existing
`TestParseImageReferences` function (lines 28-42) with:

```go
func TestParseImageReferences(t *testing.T) {
	refs := []string{
		"gcr.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
		"gcr.io/library/busybox:1.2",
		"registry-1.docker.io/library/busybox:1.36",
		"sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
		"arbitrary-ref",
		"busybox:fixed",
		"docker.io/busybox:1.36",
		"index.docker.io/library/busybox:1.36",
		"library/docker.io",
		"library/docker.io:v1",
		"busybox:docker.io",
	}
	expectedTags := []string{
		"gcr.io/library/busybox:1.2",
		"registry-1.docker.io/library/busybox:1.36",
	}
	expectedDigests := []string{"gcr.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582"}
	tags, digests := ParseImageReferences(refs)
	assert.Equal(t, expectedTags, tags)
	assert.Equal(t, expectedDigests, digests)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/cri/util/ -run TestParseImageReferences -v` Expected:
FAIL — the current implementation normalizes `busybox:fixed`,
`docker.io/busybox:1.36`, `index.docker.io/library/busybox:1.36`, and
`library/docker.io:v1` into the tag/digest output, so the slices won't match.

- [ ] **Step 3: Rewrite `ParseImageReferences` to filter by canonical form**

In `internal/cri/util/references.go`, replace the import block and the
`ParseImageReferences` function (lines 19-40) with:

```go
import (
	reference "github.com/distribution/reference"
	imagedigest "github.com/opencontainers/go-digest"
)

// ParseImageReferences parses a list of arbitrary image references and returns
// the repotags and repodigests. It surfaces only canonical references.
//
// The CRI image store keys images by their raw containerd name but displays the
// normalized form. A non-canonical name such as "busybox:fixed" would be shown
// as "docker.io/library/busybox:fixed" yet resolve to nothing, because lookups
// normalize the request before consulting the raw-keyed cache. reference.ParseNamed
// rejects any non-canonical input, so the surfaced set equals the resolvable set.
func ParseImageReferences(refs []string) ([]string, []string) {
	var tags, digests []string
	for _, ref := range refs {
		named, err := reference.ParseNamed(ref)
		if err != nil {
			continue
		}
		if _, ok := named.(reference.Canonical); ok {
			digests = append(digests, named.String())
		} else if _, ok := named.(reference.Tagged); ok {
			tags = append(tags, named.String())
		}
	}
	return tags, digests
}
```

Note: keep the `imagedigest` import — it is still used by `GetRepoDigestAndTag`
below. Do not add a `strings` import.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/cri/util/ -run TestParseImageReferences -v` Expected:
PASS

- [ ] **Step 5: Run the whole util package and vet**

Run: `go test ./internal/cri/util/... && go vet ./internal/cri/util/...`
Expected: PASS, no vet diagnostics.

- [ ] **Step 6: Commit**

```bash
git add internal/cri/util/references.go internal/cri/util/references_test.go
git diff --check
git commit -s -m "Surface only canonical image refs from CRI util

ParseImageReferences normalized references before reporting them, so a
non-canonical name like \"busybox:fixed\" was surfaced as
\"docker.io/library/busybox:fixed\" — a reference the CRI image store
cannot resolve, because it keys images by their raw name. Filter with
reference.ParseNamed so the surfaced set equals the resolvable set.

Tested: go test ./internal/cri/util/...

Assisted-by: Claude Code"
```

---

### Task 2: Hide images with no qualified references from CRI

**Files:**

- Modify: `internal/cri/server/images/image_status.go:50-79`
- Modify: `internal/cri/server/images/image_list.go:33-37`
- Test: `internal/cri/server/images/image_status_test.go`

**Interfaces:**

- Consumes: `util.ParseImageReferences` (Task 1); `imagestore.Image`;
  `imagestore.NewFakeStore([]imagestore.Image) (*Store, error)`.
- Produces: `toCRIImage(imagestore.Image) *runtime.Image` — now returns `nil`
  when the image has no canonical references. Callers must nil-check.

- [ ] **Step 1: Write the failing unit test for `toCRIImage`**

Add this function to `internal/cri/server/images/image_status_test.go` (after
`TestImageStatus`):

```go
func TestToCRIImageHidesNonCanonicalOnly(t *testing.T) {
	t.Logf("image whose only references are non-canonical is hidden")
	hidden := imagestore.Image{
		ID:         "sha256:d848ce12891bf78792cda4a23c58984033b0c397a55e93a1556202222ecc5ed4", // #nosec G101
		References: []string{"busybox:fixed", "docker.io/busybox:1.36"},
	}
	assert.Nil(t, toCRIImage(hidden))

	t.Logf("image with a canonical reference is surfaced")
	visible := imagestore.Image{
		ID:         "sha256:d848ce12891bf78792cda4a23c58984033b0c397a55e93a1556202222ecc5ed4", // #nosec G101
		References: []string{"busybox:fixed", "gcr.io/library/busybox:1.2"},
	}
	got := toCRIImage(visible)
	require.NotNil(t, got)
	assert.Equal(t, []string{"gcr.io/library/busybox:1.2"}, got.RepoTags)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run:
`go test ./internal/cri/server/images/ -run TestToCRIImageHidesNonCanonicalOnly -v`
Expected: FAIL — `toCRIImage` currently always returns a non-nil
`*runtime.Image`, so the `assert.Nil` fails.

- [ ] **Step 3: Make `toCRIImage` return nil for images with no qualified
      references**

In `internal/cri/server/images/image_status.go`, change the start of
`toCRIImage` (currently lines 62-65):

```go
// toCRIImage converts internal image object to CRI runtime.Image.
// It returns nil when the image has no canonical references, since such an
// image is not addressable through CRI.
func toCRIImage(image imagestore.Image) *runtime.Image {
	repoTags, repoDigests := util.ParseImageReferences(image.References)
	if len(repoTags) == 0 && len(repoDigests) == 0 {
		return nil
	}
	runtimeImage := &runtime.Image{
```

- [ ] **Step 4: Nil-check the result in `ImageStatus`**

In `internal/cri/server/images/image_status.go`, change the `ImageStatus` body
(currently lines 50-51) from:

```go
	runtimeImage := toCRIImage(image)
	info, err := c.toCRIImageInfo(ctx, &image, r.GetVerbose())
```

to:

```go
	runtimeImage := toCRIImage(image)
	if runtimeImage == nil {
		// Resolved by id but has no canonical references; treat as not found.
		return &runtime.ImageStatusResponse{}, nil
	}
	info, err := c.toCRIImageInfo(ctx, &image, r.GetVerbose())
```

- [ ] **Step 5: Skip nil images in `ListImages`**

In `internal/cri/server/images/image_list.go`, change the loop body (currently
lines 33-37) from:

```go
	for _, image := range imagesInStore {
		// TODO(random-liu): [P0] Make sure corresponding snapshot exists. What if snapshot
		// doesn't exist?
		images = append(images, toCRIImage(image))
	}
```

to:

```go
	for _, image := range imagesInStore {
		// TODO(random-liu): [P0] Make sure corresponding snapshot exists. What if snapshot
		// doesn't exist?
		if criImage := toCRIImage(image); criImage != nil {
			images = append(images, criImage)
		}
	}
```

- [ ] **Step 6: Run the package tests**

Run: `go test ./internal/cri/server/images/...` Expected: PASS (including the
existing `TestImageStatus` and `TestListImages`, whose images use canonical
references and are unaffected).

- [ ] **Step 7: Build the CRI plugin and vet**

Run: `go build ./internal/cri/... && go vet ./internal/cri/server/images/...`
Expected: builds clean, no vet diagnostics.

- [ ] **Step 8: Commit**

```bash
git add internal/cri/server/images/image_status.go internal/cri/server/images/image_list.go internal/cri/server/images/image_status_test.go
git diff --check
git commit -s -m "Hide images with no canonical refs from CRI

toCRIImage now returns nil when an image has no canonical references, so
ListImages skips it and ImageStatus reports it as not found. This stops
non-resolvable references (e.g. a short \"ctr tag\") from appearing in
crictl while being unusable.

Tested: go test ./internal/cri/server/images/...

Assisted-by: Claude Code"
```

---

### Task 3: Integration test for CRI visibility

**Files:**

- Create: `integration/images_visibility_test.go`

**Interfaces:**

- Consumes: package-level `containerdClient *containerd.Client`,
  `imageService *remote.ImageService`, helpers `Eventually` and `Consistently`
  (`integration/main_test.go`), `images.Get`, `images.BusyBox`.
- `imageService.ImageStatus(*runtime.ImageSpec) (*runtime.Image, error)`,
  `imageService.ListImages(*runtime.ImageFilter) ([]*runtime.Image, error)`,
  `imageService.RemoveImage(*runtime.ImageSpec) error`.
- Produces: `TestImageTagWithoutRegistryNotVisibleInCRI`.

- [ ] **Step 1: Create the integration test file**

Create `integration/images_visibility_test.go` with this exact content:

```go
/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package integration

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	coreimages "github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/integration/images"
	"github.com/containerd/errdefs"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestImageTagWithoutRegistryNotVisibleInCRI(t *testing.T) {
	baseImage := images.Get(images.BusyBox) // ghcr.io/containerd/busybox:1.36

	t.Logf("Pulling base image %s", baseImage)
	img, err := containerdClient.Pull(t.Context(), baseImage)
	require.NoError(t, err)

	t.Run("ShortTag_Hidden", func(t *testing.T) {
		shortTag := fmt.Sprintf("busybox:hidden-%s", strings.ReplaceAll(t.Name(), "/", "-"))
		t.Logf("Tagging as short tag %s", shortTag)
		createTag(t, t.Context(), img, shortTag)

		t.Logf("Verifying short tag stays hidden in CRI Status")
		require.NoError(t, Consistently(func() (bool, error) {
			criImage, err := imageService.ImageStatus(&runtime.ImageSpec{Image: shortTag})
			if err != nil {
				return false, err
			}
			return criImage == nil, nil
		}, 100*time.Millisecond, time.Second), "Short tag should not become visible in CRI Status")

		t.Logf("Verifying short tag stays hidden in CRI ListImages")
		require.NoError(t, Consistently(func() (bool, error) {
			criImages, err := imageService.ListImages(nil)
			if err != nil {
				return false, err
			}
			return !containsTag(criImages, shortTag), nil
		}, 100*time.Millisecond, time.Second), "Short tag should not be listed in CRI ListImages")
	})

	t.Run("FullTag_Visible", func(t *testing.T) {
		fullTag := fmt.Sprintf("docker.io/library/busybox:visible-%s", strings.ReplaceAll(t.Name(), "/", "-"))
		t.Logf("Tagging as full tag %s", fullTag)
		createTag(t, t.Context(), img, fullTag)

		t.Logf("Verifying full tag is visible in CRI Status")
		require.NoError(t, Eventually(func() (bool, error) {
			criImage, err := imageService.ImageStatus(&runtime.ImageSpec{Image: fullTag})
			if err != nil {
				return false, err
			}
			return criImage != nil, nil
		}, 100*time.Millisecond, 10*time.Second), "Image did not become visible in CRI status")

		t.Logf("Verifying RepoTags contains %s", fullTag)
		criImage, err := imageService.ImageStatus(&runtime.ImageSpec{Image: fullTag})
		require.NoError(t, err)
		require.NotNil(t, criImage)
		require.Contains(t, criImage.RepoTags, fullTag)

		t.Logf("Verifying full tag is visible in CRI ListImages")
		require.NoError(t, Eventually(func() (bool, error) {
			criImages, err := imageService.ListImages(nil)
			if err != nil {
				return false, err
			}
			return containsTag(criImages, fullTag), nil
		}, 100*time.Millisecond, 10*time.Second), "Image did not become listed in CRI images")
	})
}

// createTag programmatically creates a containerd image object for testing
// visibility without going through CRI. It registers cleanup for both
// containerd and CRI to prevent residual state.
func createTag(t *testing.T, ctx context.Context, img containerd.Image, tagName string) {
	t.Helper()

	// Registering cleanup before Create ensures we still attempt deletion even
	// if creation fails midway. Deleting a non-existent image is safe via
	// errdefs.IsNotFound.
	t.Cleanup(func() {
		err := imageService.RemoveImage(&runtime.ImageSpec{Image: tagName})
		if err != nil && !errdefs.IsNotFound(err) {
			require.NoError(t, err)
		}
	})

	t.Cleanup(func() {
		err := containerdClient.ImageService().Delete(context.Background(), tagName)
		if err != nil && !errdefs.IsNotFound(err) {
			require.NoError(t, err)
		}
	})

	tagImage := coreimages.Image{
		Name:   tagName,
		Target: img.Target(),
	}
	_, err := containerdClient.ImageService().Create(ctx, tagImage)
	require.NoError(t, err)
}

// containsTag checks if a list of CRI images contains a specific tag.
func containsTag(images []*runtime.Image, tag string) bool {
	for _, img := range images {
		for _, rt := range img.RepoTags {
			if rt == tag {
				return true
			}
		}
	}
	return false
}
```

- [ ] **Step 2: Verify the integration test compiles**

The `integration` package has no build tags (the Makefile builds it with
`go test -c ./integration`), so compile it directly.

Run: `go vet ./integration/...` Expected: compiles clean, no vet diagnostics.

- [ ] **Step 3: Run the targeted integration test (requires sudo — ask the
      user)**

Run: `FOCUS=TestImageTagWithoutRegistryNotVisibleInCRI make cri-integration`
Expected: PASS — `ShortTag_Hidden` stays hidden over the 1s window in both
Status and ListImages; `FullTag_Visible` appears with
`docker.io/library/busybox:visible-...` in RepoTags.

- [ ] **Step 4: Run the regression integration test (requires sudo — ask the
      user)**

Run: `FOCUS=TestContainerdImage make cri-integration` Expected: PASS — its image
`ghcr.io/containerd/busybox:1.36` is canonical, so `RepoTags == [testImage]`
still holds.

- [ ] **Step 5: Commit**

```bash
git add integration/images_visibility_test.go
git diff --check
git commit -s -m "Add CRI image visibility integration test

Covers issue #8848: a short ctr tag (no registry domain) stays hidden in
ImageStatus and ListImages, while a fully-qualified tag is surfaced with
correct RepoTags. The hidden assertions use Consistently because the CRI
image store updates asynchronously.

Tested: FOCUS=TestImageTagWithoutRegistryNotVisibleInCRI make cri-integration

Assisted-by: Claude Code"
```

---

## Notes for the implementer

- Do not touch `RemoveImage`, the image store, or the event subscription. Hiding
  lives entirely on the read surface.
- An image is still resolvable by its image ID internally; that is intentional
  and not user-facing through `crictl images`.
- A name carrying both a tag and a digest is an unusual containerd image-name
  form not produced by pull/tag flows; its handling is unchanged.
