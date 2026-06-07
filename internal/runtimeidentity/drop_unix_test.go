//go:build unix

package runtimeidentity

import (
	"errors"
	"reflect"
	"testing"
)

func TestDropToContainerNonrootIfRootDropsRootToDistrolessUser(t *testing.T) {
	var calls []string
	var groups []int
	var gid int
	var uid int

	err := dropToContainerNonroot(dropOps{
		geteuid: func() int { return 0 },
		setgroups: func(value []int) error {
			calls = append(calls, "setgroups")
			groups = append([]int(nil), value...)
			return nil
		},
		setgid: func(value int) error {
			calls = append(calls, "setgid")
			gid = value
			return nil
		},
		setuid: func(value int) error {
			calls = append(calls, "setuid")
			uid = value
			return nil
		},
	})
	if err != nil {
		t.Fatalf("dropToContainerNonroot: %v", err)
	}
	if !reflect.DeepEqual(calls, []string{"setgroups", "setgid", "setuid"}) {
		t.Fatalf("calls = %#v", calls)
	}
	if !reflect.DeepEqual(groups, []int{DistrolessNonrootGID}) {
		t.Fatalf("groups = %#v", groups)
	}
	if gid != DistrolessNonrootGID || uid != DistrolessNonrootUID {
		t.Fatalf("gid/uid = %d/%d, want %d/%d", gid, uid, DistrolessNonrootGID, DistrolessNonrootUID)
	}
}

func TestDropToContainerNonrootIfRootNoopsWhenAlreadyNonroot(t *testing.T) {
	called := false
	err := dropToContainerNonroot(dropOps{
		geteuid: func() int { return DistrolessNonrootUID },
		setgroups: func([]int) error {
			called = true
			return nil
		},
		setgid: func(int) error {
			called = true
			return nil
		},
		setuid: func(int) error {
			called = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("dropToContainerNonroot: %v", err)
	}
	if called {
		t.Fatal("dropToContainerNonroot called setuid/setgid while already nonroot")
	}
}

func TestDropToContainerNonrootIfRootStopsOnGroupFailure(t *testing.T) {
	wantErr := errors.New("groups unavailable")
	err := dropToContainerNonroot(dropOps{
		geteuid:   func() int { return 0 },
		setgroups: func([]int) error { return wantErr },
		setgid:    func(int) error { t.Fatal("setgid called after setgroups failure"); return nil },
		setuid:    func(int) error { t.Fatal("setuid called after setgroups failure"); return nil },
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}
