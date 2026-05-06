//go:build !unix

package exportgateway

import "os"

func linkCount(info os.FileInfo) uint64 {
	return 0
}
