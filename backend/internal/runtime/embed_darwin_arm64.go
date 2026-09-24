//go:build darwin && arm64

package runtime

import "embed"

//go:embed python_assets/python_manifest.json python_assets/python-darwin-arm64.tar.gz
var pythonAssetsFS embed.FS
