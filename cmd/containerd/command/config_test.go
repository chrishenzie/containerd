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

package command

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	srvconfig "github.com/containerd/containerd/v2/cmd/containerd/server/config"
	"github.com/containerd/containerd/v2/version"
)

func TestLoadConfigRootRequirement(t *testing.T) {
	tempDir := t.TempDir()
	dropinDir := filepath.Join(tempDir, "conf.d")
	require.NoError(t, os.MkdirAll(dropinDir, 0o700))
	dropin := fmt.Sprintf("version = %d\nroot = \"from-dropin\"\n", version.ConfigVersion)
	require.NoError(t, os.WriteFile(filepath.Join(dropinDir, "default.toml"), []byte(dropin), 0o600))
	rootPath := filepath.Join(tempDir, "config.toml")

	config := &srvconfig.Config{
		Version: version.ConfigVersion,
		Root:    "default-root",
		Imports: []string{"conf.d/*.toml"},
	}
	require.NoError(t, loadConfig(context.Background(), rootPath, false, config))
	assert.Equal(t, "from-dropin", config.Root)

	err := loadConfig(context.Background(), rootPath, true, config)
	require.ErrorIs(t, err, os.ErrNotExist)
}
