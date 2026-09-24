//go:build linux && arm64

package runtime

import "embed"

//go:embed python_assets/python_manifest.json python_assets/python-linux-arm64.tar.gz
var pythonAssetsFS embed.FS
