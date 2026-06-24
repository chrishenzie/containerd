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

package util

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

// GetRepoDigestAndTag returns image repoDigest and repoTag of the named image reference.
func GetRepoDigestAndTag(namedRef reference.Named, digest imagedigest.Digest) (string, string) {
	var repoTag, repoDigest string
	if _, ok := namedRef.(reference.NamedTagged); ok {
		repoTag = namedRef.String()
	}
	if _, ok := namedRef.(reference.Canonical); ok {
		repoDigest = namedRef.String()
	} else {
		repoDigest = namedRef.Name() + "@" + digest.String()
	}
	return repoDigest, repoTag
}
