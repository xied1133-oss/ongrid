//go:build linux

package host_files

import (
	"os"
	"syscall"
	"time"
)

// fileTimes returns mtime and atime for the given FileInfo on Linux.
// On Linux syscall.Stat_t's atime field is named Atim (timespec); on
// Darwin it's Atimespec. The build-tagged variants of this function
// keep the handler in handlers.go portable across the two OSes.
func fileTimes(fi os.FileInfo) (time.Time, time.Time) {
	mtime := fi.ModTime().UTC()
	atime := mtime
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		atime = time.Unix(st.Atim.Sec, st.Atim.Nsec).UTC()
	}
	return mtime, atime
}
