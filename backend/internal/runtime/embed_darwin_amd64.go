//go:build darwin && amd64

package runtime

import "embed"

//go:embed python_assets/python_manifest.json python_assets/python-darwin-amd64.tar.gz
var pythonAssetsFS embed.FS
