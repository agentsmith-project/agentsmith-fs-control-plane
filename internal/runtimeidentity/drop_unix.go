//go:build unix

package runtimeidentity

import (
	"fmt"
	"os"
	"syscall"
)

const (
	DistrolessNonrootUID = 65532
	DistrolessNonrootGID = 65532
)

type dropOps struct {
	geteuid   func() int
	setgroups func([]int) error
	setgid    func(int) error
	setuid    func(int) error
}

func DropToContainerNonrootIfRoot() error {
	return dropToContainerNonroot(dropOps{
		geteuid:   os.Geteuid,
		setgroups: syscall.Setgroups,
		setgid:    syscall.Setgid,
		setuid:    syscall.Setuid,
	})
}

func dropToContainerNonroot(ops dropOps) error {
	if ops.geteuid == nil || ops.setgroups == nil || ops.setgid == nil || ops.setuid == nil {
		return fmt.Errorf("runtime identity drop operations are required")
	}
	if ops.geteuid() != 0 {
		return nil
	}
	if err := ops.setgroups([]int{DistrolessNonrootGID}); err != nil {
		return fmt.Errorf("set supplementary groups: %w", err)
	}
	if err := ops.setgid(DistrolessNonrootGID); err != nil {
		return fmt.Errorf("set gid: %w", err)
	}
	if err := ops.setuid(DistrolessNonrootUID); err != nil {
		return fmt.Errorf("set uid: %w", err)
	}
	return nil
}
