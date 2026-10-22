//go:build unix

package importsqlite

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"syscall"
)

// shmDMS is the byte of a WAL database's -shm file that every open SQLite connection holds a read lock on for as long as it is
// open (UNIX_SHM_DMS in SQLite's os_unix.c: (22+8)*4 + 8). A write lock on it that another process holds or would block is a
// connection of that process.
const shmDMS = 128

// checkNoOtherProcess refuses a WAL database that another process has open: such a process holds a lock on the DMS byte of
// the -shm. It asks with F_GETLK, which takes no lock, and only opens the -shm for reading. A database without a -shm has no
// open WAL connection (the procedure copies the file and its -wal; SQLite makes a new -shm when it opens the copy).
//
// The check runs before the importer's own connection is opened: POSIX locks belong to the process, so a lock of the
// importer's own process would not show, and closing the descriptor here drops every lock this process holds on the file.
// It sees other processes on the same machine only; a server on another node that has the file on a network volume does not
// show, which is why the runbook scales the server to zero first.
func checkNoOtherProcess(path string) error {
	f, err := os.Open(path + "-shm")
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check the SQLite file's -shm: %w", err)
	}
	defer f.Close()
	lk := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: io.SeekStart, Start: shmDMS, Len: 1}
	if err := syscall.FcntlFlock(f.Fd(), syscall.F_GETLK, &lk); err != nil {
		return fmt.Errorf("check the SQLite file's -shm: %w", err)
	}
	if lk.Type != syscall.F_UNLCK {
		return ErrSourceBusy
	}
	return nil
}
