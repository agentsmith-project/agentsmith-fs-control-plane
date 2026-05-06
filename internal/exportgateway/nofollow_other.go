//go:build !unix

package exportgateway

import (
	"context"
	"errors"
	"os"

	"golang.org/x/net/webdav"
)

var errNoFollowUnsupported = errors.New("no-follow WebDAV filesystem is unsupported on this platform")

type noFollowFileSystem struct{}

func newNoFollowFileSystem(root string) (*noFollowFileSystem, error) {
	return nil, errNoFollowUnsupported
}

func (fs *noFollowFileSystem) Close() error {
	return nil
}

func (fs *noFollowFileSystem) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	return errNoFollowUnsupported
}

func (fs *noFollowFileSystem) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	return nil, errNoFollowUnsupported
}

func (fs *noFollowFileSystem) RemoveAll(ctx context.Context, name string) error {
	return errNoFollowUnsupported
}

func (fs *noFollowFileSystem) Rename(ctx context.Context, oldName, newName string) error {
	return errNoFollowUnsupported
}

func (fs *noFollowFileSystem) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	return nil, errNoFollowUnsupported
}
