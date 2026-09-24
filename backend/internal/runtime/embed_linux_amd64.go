//go:build linux && amd64

package runtime

import "embed"

//go:embed python_assets/python_manifest.json python_assets/python-linux-amd64.tar.gz
var pythonAssetsFS embed.FS
