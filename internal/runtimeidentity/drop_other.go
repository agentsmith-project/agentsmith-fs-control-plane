//go:build !unix

package runtimeidentity

const (
	DistrolessNonrootUID = 65532
	DistrolessNonrootGID = 65532
)

func DropToContainerNonrootIfRoot() error {
	return nil
}
