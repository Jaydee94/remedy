//go:build !unix

package importsqlite

import "errors"

// checkNoOtherProcess cannot ask for SQLite's locks here, so it refuses: the importer runs on Linux and macOS.
func checkNoOtherProcess(string) error {
	return errors.New("the importer cannot check on this system whether another process has the SQLite file open; run it on Linux or macOS")
}
