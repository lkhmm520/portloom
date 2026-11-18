package cutover

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/lkhmm520/portloom/internal/store"
	"golang.org/x/sys/unix"
)

const journalLimit = 8 << 20

// FileJournal serializes each operation on one Linux host. Use an existing,
// durably provisioned 0700 directory on a local filesystem that supports flock,
// atomic rename and fsync. The effective UID and directory ancestors are
// trusted; never move the directory or remove its lock/record files in use.
type FileJournal struct {
	path     string
	identity os.FileInfo
}

var _ SwitchJournal = (*FileJournal)(nil)

func OpenFileJournal(dir string) (*FileJournal, error) {
	if dir == "" {
		return nil, fmt.Errorf("%w: journal directory is required", ErrInput)
	}
	path, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	j := &FileJournal{path: path}
	f, info, err := j.openDir()
	if err != nil {
		return nil, err
	}
	defer f.Close()
	j.identity = info
	return j, nil
}

func (j *FileJournal) openDir() (*os.File, os.FileInfo, error) {
	fd, err := unix.Open(j.path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open journal directory: %w", err)
	}
	f := os.NewFile(uintptr(fd), "journal directory")
	info, err := f.Stat()
	var st unix.Stat_t
	if err != nil || unix.Fstat(fd, &st) != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&07777 != 0700 || (j.identity != nil && !os.SameFile(j.identity, info)) {
		f.Close()
		return nil, nil, errors.New("journal directory must be stable, owned by the effective UID and mode 0700")
	}
	return f, info, nil
}

func journalFile(dir *os.File, name string, flags int) (*os.File, error) {
	fd, err := unix.Openat(int(dir.Fd()), name, flags|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&07777 != 0600 || st.Nlink != 1 || st.Uid != uint32(os.Geteuid()) {
		unix.Close(fd)
		return nil, errors.New("journal entry must be a private, owned regular file with one link")
	}
	return os.NewFile(uintptr(fd), name), nil
}

type journalRecord struct {
	Version  int             `json:"version"`
	Digest   string          `json:"sha256"`
	Progress json.RawMessage `json:"progress"`
}

func (j *FileJournal) WithOperation(ctx context.Context, id string, run func(*SwitchProgress, func(SwitchProgress) error) error) error {
	if !validSwitchID(id) || !utf8.ValidString(id) || run == nil || j.identity == nil {
		return fmt.Errorf("%w: invalid journal operation", ErrInput)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, _, err := j.openDir()
	if err != nil {
		return err
	}
	defer dir.Close()
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(id)))
	lock, err := journalFile(dir, key+".lock", unix.O_CREAT|unix.O_RDWR)
	if err != nil {
		return fmt.Errorf("open operation lock: %w", err)
	}
	// Closing the separate, never-replaced lock inode releases the flock even
	// on process death; atomic replacements only touch the record inode.
	defer lock.Close()
	if err := lockOperation(ctx, lock); err != nil {
		return err
	}
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync journal directory before recovery: %w", err)
	}
	name := key + ".json"
	current, err := readJournal(dir, name, id)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var spec *SwitchSpec
	if current != nil {
		saved := cloneProgress(*current).Spec
		spec = &saved
	}
	var mu sync.Mutex
	active := true
	var saveErr error
	deactivate := func() { mu.Lock(); active = false; mu.Unlock() }
	defer deactivate()
	runErr := run(current, func(p SwitchProgress) error {
		mu.Lock()
		defer mu.Unlock()
		if !active {
			return errors.New("operation lock is no longer held")
		}
		if saveErr != nil {
			return saveErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !validJournalProgress(p, id) || (spec != nil && !reflect.DeepEqual(*spec, p.Spec)) {
			saveErr = fmt.Errorf("%w: invalid progress or rebound operation ID", ErrSwitchConflict)
			return saveErr
		}
		if err := writeJournal(dir, name, p); err != nil {
			saveErr = err
			return err
		}
		saved := cloneProgress(p).Spec
		spec = &saved
		return nil
	})
	deactivate()
	if runErr != nil {
		return runErr
	}
	return saveErr
}

func lockOperation(ctx context.Context, lock *os.File) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, unix.EINTR) && !errors.Is(err, unix.EWOULDBLOCK) {
			return fmt.Errorf("lock journal operation: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func readJournal(dir *os.File, name, id string) (*SwitchProgress, error) {
	f, err := journalFile(dir, name, unix.O_RDONLY)
	if errors.Is(err, unix.ENOENT) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open journal progress: %w", err)
	}
	defer f.Close()
	data, err := readLimited(f, journalLimit)
	var record journalRecord
	var progress SwitchProgress
	if err != nil || !utf8.Valid(data) || !json.Valid(data) || !uniqueConfigKeys(json.NewDecoder(bytes.NewReader(data))) || json.Unmarshal(data, &record) != nil || record.Version != 1 ||
		record.Digest != fmt.Sprintf("%x", sha256.Sum256(record.Progress)) || json.Unmarshal(record.Progress, &progress) != nil || !validJournalProgress(progress, id) {
		return nil, fmt.Errorf("%w: invalid journal record", ErrSwitchConflict)
	}
	return &progress, nil
}

func validJournalProgress(p SwitchProgress, id string) bool {
	normalized, err := normalizeSwitchSpec(p.Spec)
	if err != nil || p.Spec.OperationID != id || !reflect.DeepEqual(normalized, p.Spec) || p.Traffic.Generation < 0 {
		return false
	}
	for _, gateSet := range []map[string]store.PublicationGate{p.SourceGates, p.TargetGates} {
		if len(gateSet) != len(p.Spec.Hosts) {
			return false
		}
		for _, host := range p.Spec.Hosts {
			if g, ok := gateSet[host]; !ok || !validObservedGate(g) {
				return false
			}
		}
	}
	switch p.Status {
	case SwitchPrepared, SwitchSourceBlocked, SwitchTargetPromoted, SwitchTrafficChanged, SwitchComplete, SwitchRestoring, SwitchRolledBack:
	default:
		return false
	}
	if p.Pending != "" {
		_, _, err := (&Coordinator{}).transition(p)
		return err == nil
	}
	return true
}

func writeJournal(dir *os.File, name string, progress SwitchProgress) error {
	data, err := json.Marshal(progress)
	if err != nil {
		return errors.New("encode journal progress")
	}
	record, err := json.Marshal(journalRecord{Version: 1, Digest: fmt.Sprintf("%x", sha256.Sum256(data)), Progress: data})
	if err != nil || len(record) > journalLimit {
		return errors.New("journal progress exceeds record limit")
	}
	temp := ".pending-" + rand.Text()
	f, err := journalFile(dir, temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL)
	if err != nil {
		return fmt.Errorf("create temporary journal record: %w", err)
	}
	dst := int(dir.Fd())
	defer unix.Unlinkat(dst, temp, 0)
	_, writeErr := f.Write(record)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return fmt.Errorf("persist journal record: %w", err)
	}
	if err := unix.Renameat(dst, temp, dst, name); err != nil {
		return fmt.Errorf("replace journal record: %w", err)
	}
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync journal directory after replace: %w", err)
	}
	return nil
}
