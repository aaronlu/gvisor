// Copyright 2019 The gVisor Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package vfs

import (
	"gvisor.dev/gvisor/pkg/abi/linux"
)

// GetDentryOptions contains options to VirtualFilesystem.GetDentryAt() and
// FilesystemImpl.GetDentryAt().
type GetDentryOptions struct {
	// If CheckSearchable is true, FilesystemImpl.GetDentryAt() must check that
	// the returned Dentry is a directory for which creds has search
	// permission.
	CheckSearchable bool
}

// MkdirOptions contains options to VirtualFilesystem.MkdirAt() and
// FilesystemImpl.MkdirAt().
type MkdirOptions struct {
	// Mode is the file mode bits for the created directory.
	Mode linux.FileMode
}

// MknodOptions contains options to VirtualFilesystem.MknodAt() and
// FilesystemImpl.MknodAt().
type MknodOptions struct {
	// Mode is the file type and mode bits for the created file.
	Mode linux.FileMode

	// If Mode specifies a character or block device special file, DevMajor and
	// DevMinor are the major and minor device numbers for the created device.
	DevMajor uint32
	DevMinor uint32
}

// MountFlags contains flags as specified for mount(2), e.g. MS_NOEXEC,
// MS_RDONLY.
type MountFlags uint32

// NoExec returns true if MS_NOEXEC is set.
func (f MountFlags) NoExec() bool {
	return f&linux.MS_NOEXEC != 0
}

// ReadOnly returns true if MS_RDONLY is set.
func (f MountFlags) ReadOnly() bool {
	return f&linux.MS_RDONLY != 0
}

// NoAccessTime returns true if MS_NOATIME is set.
//
// TODO(gvisor.dev/issue/1193): Implement MS_NOATIME.
func (f MountFlags) NoAccessTime() bool {
	return f&linux.MS_NOATIME != 0
}

// MountOptions contains options to VirtualFilesystem.MountAt().
type MountOptions struct {
	// Flags contains flags as specified for mount(2), e.g. MS_NOEXEC, MS_RDONLY.
	Flags MountFlags

	// GetFilesystemOptions contains options to FilesystemType.GetFilesystem().
	GetFilesystemOptions GetFilesystemOptions

	// If InternalMount is true, allow the use of filesystem types for which
	// RegisterFilesystemTypeOptions.AllowUserMount == false.
	InternalMount bool
}

// OpenOptions contains options to VirtualFilesystem.OpenAt() and
// FilesystemImpl.OpenAt().
type OpenOptions struct {
	// Flags contains access mode and flags as specified for open(2).
	//
	// FilesystemImpls are responsible for implementing the following flags:
	// O_RDONLY, O_WRONLY, O_RDWR, O_APPEND, O_CREAT, O_DIRECT, O_DSYNC,
	// O_EXCL, O_NOATIME, O_NOCTTY, O_NONBLOCK, O_PATH, O_SYNC, O_TMPFILE, and
	// O_TRUNC. VFS is responsible for handling O_DIRECTORY, O_LARGEFILE, and
	// O_NOFOLLOW. VFS users are responsible for handling O_CLOEXEC, since file
	// descriptors are mostly outside the scope of VFS.
	Flags uint32

	// If FilesystemImpl.OpenAt() creates a file, Mode is the file mode for the
	// created file.
	Mode linux.FileMode

	// FileExec is set when the file is being opened to be executed.
	// FileSystem.OpenAt() checks that the caller has execute permissions on the
	// file, that the file is a regular file, and that the mount doesn't have
	// MS_NOEXEC set.
	FileExec bool
}

// ReadOptions contains options to FileDescription.PRead(),
// FileDescriptionImpl.PRead(), FileDescription.Read(), and
// FileDescriptionImpl.Read().
type ReadOptions struct {
	// Flags contains flags as specified for preadv2(2).
	Flags uint32
}

// RenameOptions contains options to VirtualFilesystem.RenameAt() and
// FilesystemImpl.RenameAt().
type RenameOptions struct {
	// Flags contains flags as specified for renameat2(2).
	Flags uint32

	// If MustBeDir is true, the renamed file must be a directory.
	MustBeDir bool
}

// SetStatOptions contains options to VirtualFilesystem.SetStatAt(),
// FilesystemImpl.SetStatAt(), FileDescription.SetStat(), and
// FileDescriptionImpl.SetStat().
type SetStatOptions struct {
	// Stat is the metadata that should be set. Only fields indicated by
	// Stat.Mask should be set.
	//
	// If Stat specifies that a timestamp should be set,
	// FilesystemImpl.SetStatAt() and FileDescriptionImpl.SetStat() must
	// special-case StatxTimestamp.Nsec == UTIME_NOW as described by
	// utimensat(2); however, they do not need to check for StatxTimestamp.Nsec
	// == UTIME_OMIT (VFS users must unset the corresponding bit in Stat.Mask
	// instead).
	Stat linux.Statx
}

// SetxattrOptions contains options to VirtualFilesystem.SetxattrAt(),
// FilesystemImpl.SetxattrAt(), FileDescription.Setxattr(), and
// FileDescriptionImpl.Setxattr().
type SetxattrOptions struct {
	// Name is the name of the extended attribute being mutated.
	Name string

	// Value is the extended attribute's new value.
	Value string

	// Flags contains flags as specified for setxattr/lsetxattr/fsetxattr(2).
	Flags uint32
}

// StatOptions contains options to VirtualFilesystem.StatAt(),
// FilesystemImpl.StatAt(), FileDescription.Stat(), and
// FileDescriptionImpl.Stat().
type StatOptions struct {
	// Mask is the set of fields in the returned Statx that the FilesystemImpl
	// or FileDescriptionImpl should provide. Bits are as in linux.Statx.Mask.
	//
	// The FilesystemImpl or FileDescriptionImpl may return fields not
	// requested in Mask, and may fail to return fields requested in Mask that
	// are not supported by the underlying filesystem implementation, without
	// returning an error.
	Mask uint32

	// Sync specifies the synchronization required, and is one of
	// linux.AT_STATX_SYNC_AS_STAT (which is 0, and therefore the default),
	// linux.AT_STATX_SYNC_FORCE_SYNC, or linux.AT_STATX_SYNC_DONT_SYNC.
	Sync uint32
}

// UmountOptions contains options to VirtualFilesystem.UmountAt().
type UmountOptions struct {
	// Flags contains flags as specified for umount2(2).
	Flags uint32
}

// WriteOptions contains options to FileDescription.PWrite(),
// FileDescriptionImpl.PWrite(), FileDescription.Write(), and
// FileDescriptionImpl.Write().
type WriteOptions struct {
	// Flags contains flags as specified for pwritev2(2).
	Flags uint32
}
