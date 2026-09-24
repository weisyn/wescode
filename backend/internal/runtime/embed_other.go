//go:build !((darwin && arm64) || (darwin && amd64) || (linux && amd64) || (linux && arm64) || (windows && amd64))

package runtime

import "embed"

//go:embed python_assets/.gitkeep
var pythonAssetsFS embed.FS
