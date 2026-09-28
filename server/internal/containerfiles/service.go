package containerfiles

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/suma/suma/server/internal/container"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/secret"
	"gorm.io/gorm"
)

const MaxTextBytes = 2 << 20
const historyBudget = 512 << 20

var ErrConflict = errors.New("file changed since it was opened")
var ErrUnsupported = errors.New("file operation is unsupported for this path")

type Runtime interface {
	Get(context.Context, string) (container.Detail, error)
	RunFileCommand(context.Context, string, string, []string, []byte, int) ([]byte, error)
}

type MountInfo struct {
	Type        string `json:"type"`
	Name        string `json:"name,omitempty"`
	Source      string `json:"source,omitempty"`
	Destination string `json:"destination"`
	ReadWrite   bool   `json:"read_write"`
	IsDirectory bool   `json:"is_directory"`
}
type Entry struct {
	Name       string     `json:"name"`
	Path       string     `json:"path"`
	Type       string     `json:"type"`
	Size       int64      `json:"size"`
	ModifiedAt int64      `json:"modified_at"`
	Mount      *MountInfo `json:"mount,omitempty"`
}
type Listing struct {
	Path         string      `json:"path"`
	Entries      []Entry     `json:"entries"`
	NextCursor   string      `json:"next_cursor,omitempty"`
	Mounts       []MountInfo `json:"mounts"`
	CurrentMount *MountInfo  `json:"current_mount,omitempty"`
}
type Content struct {
	Path           string `json:"path"`
	Content        string `json:"content"`
	ETag           string `json:"etag"`
	Persistent     bool   `json:"persistent"`
	ReadOnly       bool   `json:"read_only"`
	SingleFileBind bool   `json:"single_file_bind"`
	Warning        string `json:"warning,omitempty"`
}
type Revision struct {
	ID        uint      `json:"id"`
	Hash      string    `json:"hash"`
	UserID    *uint     `json:"user_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Baseline  bool      `json:"baseline"`
}
type Action struct {
	Action string `json:"action"`
	Path   string `json:"path"`
	Target string `json:"target,omitempty"`
}

type Service struct {
	db      *gorm.DB
	secrets *secret.Store
	mu      sync.Mutex
}

func NewService(db *gorm.DB, secrets *secret.Store) *Service {
	return &Service{db: db, secrets: secrets}
}

func CleanPath(value string) (string, error) {
	if value == "" || !strings.HasPrefix(value, "/") || len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n") || path.Clean(value) != value {
		return "", errors.New("a normalized absolute container path is required")
	}
	return value, nil
}

func (s *Service) inspect(ctx context.Context, runtime Runtime, id string) (container.Detail, error) {
	row, err := runtime.Get(ctx, id)
	if err != nil {
		return row, err
	}
	if row.State != "running" {
		return row, errors.New("file tools require a running container")
	}
	return row, nil
}

func mounts(row container.Detail) []MountInfo {
	result := make([]MountInfo, 0, len(row.Mounts))
	for _, mount := range row.Mounts {
		if mount.Type == "bind" || mount.Type == "volume" || mount.Type == "tmpfs" {
			result = append(result, MountInfo{Type: mount.Type, Name: mount.Name, Source: mount.Source, Destination: path.Clean(mount.Destination), ReadWrite: mount.ReadWrite})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Destination < result[j].Destination })
	return result
}

func effectiveMount(all []MountInfo, name string) *MountInfo {
	var found *MountInfo
	for i := range all {
		m := &all[i]
		if name == m.Destination || strings.HasPrefix(name, strings.TrimSuffix(m.Destination, "/")+"/") {
			if found == nil || len(m.Destination) > len(found.Destination) {
				found = m
			}
		}
	}
	return found
}

const noSymlinkScript = `p=$1; while [ "$p" != / ]; do [ ! -L "$p" ] || { echo 'symbolic-link paths are not supported' >&2; exit 70; }; p=${p%/*}; [ -n "$p" ] || p=/; done`

func guardPath(ctx context.Context, runtime Runtime, id, name string) error {
	_, err := runtime.RunFileCommand(ctx, id, noSymlinkScript, []string{name}, nil, 1024)
	return err
}

const listScript = `LC_ALL=C; export LC_ALL; [ -d "$1" ] || exit 2; for f in "$1"/* "$1"/.[!.]* "$1"/..?*; do [ -e "$f" ] || [ -L "$f" ] || continue; if [ -L "$f" ]; then kind=link; elif [ -d "$f" ]; then kind=directory; elif [ -f "$f" ]; then kind=file; else kind=special; fi; size=$(stat -c %s "$f" 2>/dev/null) || size=0; modified=$(stat -c %Y "$f" 2>/dev/null) || modified=0; printf '%s\000%s\000%s\000%s\000' "${f##*/}" "$kind" "$size" "$modified"; done`

func (s *Service) List(ctx context.Context, runtime Runtime, id, folder, cursor string) (Listing, error) {
	name, err := CleanPath(folder)
	if err != nil {
		return Listing{}, err
	}
	row, err := s.inspect(ctx, runtime, id)
	if err != nil {
		return Listing{}, err
	}
	if err := guardPath(ctx, runtime, id, name); err != nil {
		return Listing{}, err
	}
	data, err := runtime.RunFileCommand(ctx, id, listScript, []string{name}, nil, 4<<20)
	if err != nil {
		return Listing{}, err
	}
	parts := strings.Split(string(data), "\x00")
	if len(parts) == 0 || parts[len(parts)-1] != "" || (len(parts)-1)%4 != 0 {
		return Listing{}, errors.New("invalid directory listing from container")
	}
	all := mounts(row)
	for i := range all {
		kind, kindErr := runtime.RunFileCommand(ctx, id, `[ -d "$1" ] && printf 1 || printf 0`, []string{all[i].Destination}, nil, 1)
		if kindErr == nil {
			all[i].IsDirectory = string(kind) == "1"
		}
	}
	result := Listing{Path: name, Entries: []Entry{}, Mounts: all, CurrentMount: effectiveMount(all, name)}
	entries := make([]Entry, 0, (len(parts)-1)/4)
	for i := 0; i+3 < len(parts)-1; i += 4 {
		entryName := parts[i]
		if !utf8.ValidString(entryName) {
			continue
		}
		size, _ := strconv.ParseInt(parts[i+2], 10, 64)
		modified, _ := strconv.ParseInt(parts[i+3], 10, 64)
		itemPath := path.Join(name, entryName)
		entries = append(entries, Entry{Name: entryName, Path: itemPath, Type: parts[i+1], Size: size, ModifiedAt: modified, Mount: effectiveMount(all, itemPath)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	for _, entry := range entries {
		if entry.Name <= cursor {
			continue
		}
		if len(result.Entries) == 200 {
			result.NextCursor = result.Entries[199].Name
			break
		}
		result.Entries = append(result.Entries, entry)
	}
	return result, nil
}

const readScript = `[ -f "$1" ] && [ ! -L "$1" ] || exit 2; cat -- "$1"`

func hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (s *Service) Read(ctx context.Context, runtime Runtime, id, file string) (Content, error) {
	name, err := CleanPath(file)
	if err != nil {
		return Content{}, err
	}
	row, err := s.inspect(ctx, runtime, id)
	if err != nil {
		return Content{}, err
	}
	if err := guardPath(ctx, runtime, id, name); err != nil {
		return Content{}, err
	}
	data, err := runtime.RunFileCommand(ctx, id, readScript, []string{name}, nil, MaxTextBytes+1)
	if err != nil {
		return Content{}, err
	}
	if len(data) > MaxTextBytes || !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
		return Content{}, errors.New("only UTF-8 text files up to 2 MiB can be edited")
	}
	value := string(data)
	mount := effectiveMount(mounts(row), name)
	persistent := mount != nil && (mount.Type == "bind" || mount.Type == "volume")
	readOnly := row.ReadOnlyRootFS
	if mount != nil {
		readOnly = !mount.ReadWrite
	}
	singleFile := mount != nil && mount.Type == "bind" && mount.Destination == name
	return Content{Path: name, Content: value, ETag: hash(value), Persistent: persistent, ReadOnly: readOnly, SingleFileBind: singleFile}, nil
}

// ReadForNode resolves interrupted saves before returning editor content. An
// unexpected observed hash remains readable so an operator can inspect it.
func (s *Service) ReadForNode(ctx context.Context, runtime Runtime, nodeID, id, file string) (Content, error) {
	current, err := s.Read(ctx, runtime, id, file)
	if err != nil {
		return current, err
	}
	var pending []database.FileRevision
	if err := s.db.WithContext(ctx).Where("node_id = ? AND container_id = ? AND path = ? AND state = ?", nodeID, id, current.Path, "pending").Find(&pending).Error; err != nil {
		return current, err
	}
	if len(pending) == 0 {
		var failed int64
		_ = s.db.WithContext(ctx).Model(&database.FileRevision{}).Where("node_id = ? AND container_id = ? AND path = ? AND state = ?", nodeID, id, current.Path, "failed").Count(&failed).Error
		if failed > 0 {
			var latest database.FileRevision
			if err := s.db.WithContext(ctx).Where("node_id = ? AND container_id = ? AND path = ? AND state = ?", nodeID, id, current.Path, "committed").Order("id DESC").First(&latest).Error; err == nil && current.ETag != latest.Hash {
				current.Warning = "An interrupted save left content that does not match a verified revision; inspect the file before saving or restoring."
			}
		}
		return current, nil
	}
	var latest database.FileRevision
	_ = s.db.WithContext(ctx).Where("node_id = ? AND container_id = ? AND path = ? AND state = ?", nodeID, id, current.Path, "committed").Order("id DESC").First(&latest).Error
	for _, row := range pending {
		if current.ETag != row.Hash && current.ETag != latest.Hash {
			current.Warning = "An interrupted save left content that does not match a verified revision; inspect the file before saving or restoring."
			break
		}
	}
	if err := s.reconcile(ctx, runtime, nodeID, id, current.Path); err != nil {
		return current, err
	}
	return current, nil
}

const writeScript = `umask 077; cat > "$1"`
const createTempScript = `umask 077; set -C; : > "$1"`
const checkHashScript = `actual=$(sha256sum "$1") || exit 74; actual=${actual%% *}; [ "$actual" = "$2" ] || { echo 'file changed since it was opened' >&2; exit 75; }`
const writeCheckedScript = checkHashScript + `; umask 077; cat > "$1"`
const replaceScript = `old=$1; staged=$2; [ -f "$old" ] && [ ! -L "$old" ] || exit 2; actual=$(sha256sum "$old") || exit 74; actual=${actual%% *}; [ "$actual" = "$3" ] || { echo 'file changed since it was opened' >&2; exit 75; }; [ "$(id -u)" = "$(stat -c %u "$old")" ] || { echo 'file owner differs from container user' >&2; exit 73; }; mode=$(stat -c %a "$old") || exit; group=$(stat -c %g "$old") || exit; chgrp "$group" "$staged" || exit; chmod "$mode" "$staged" || exit; mv -f "$staged" "$old"`

func (s *Service) write(ctx context.Context, runtime Runtime, id string, current Content, value string) error {
	if current.SingleFileBind {
		_, err := runtime.RunFileCommand(ctx, id, writeCheckedScript, []string{current.Path, current.ETag}, []byte(value), 1024)
		if err != nil && strings.Contains(err.Error(), ErrConflict.Error()) {
			return ErrConflict
		}
		return err
	}
	temp := fmt.Sprintf("%s/.suma-edit-%d", path.Dir(current.Path), time.Now().UnixNano())
	if _, err := runtime.RunFileCommand(ctx, id, createTempScript, []string{temp}, nil, 1024); err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = runtime.RunFileCommand(cleanupCtx, id, `rm -f -- "$1"`, []string{temp}, nil, 1024)
	}()
	if _, err := runtime.RunFileCommand(ctx, id, writeScript, []string{temp}, []byte(value), 1024); err != nil {
		return err
	}
	_, err := runtime.RunFileCommand(ctx, id, replaceScript, []string{current.Path, temp, current.ETag}, nil, 1024)
	if err != nil && strings.Contains(err.Error(), ErrConflict.Error()) {
		return ErrConflict
	}
	return err
}

func (s *Service) Save(ctx context.Context, runtime Runtime, nodeID, id, file, expected, value string, userID *uint) (Content, error) {
	return s.save(ctx, runtime, nodeID, id, file, expected, value, userID, false)
}

func (s *Service) save(ctx context.Context, runtime Runtime, nodeID, id, file, expected, value string, userID *uint, forceRevision bool) (Content, error) {
	if len(value) > MaxTextBytes || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return Content{}, errors.New("only UTF-8 text files up to 2 MiB can be saved")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.Read(ctx, runtime, id, file)
	if err != nil {
		return Content{}, err
	}
	if current.ReadOnly {
		return Content{}, ErrUnsupported
	}
	if current.ETag != expected {
		return Content{}, ErrConflict
	}
	if current.Content == value && !forceRevision {
		return current, nil
	}
	if err := s.reconcile(ctx, runtime, nodeID, id, file); err != nil {
		return Content{}, err
	}
	baselineCount := int64(0)
	if err := s.db.WithContext(ctx).Model(&database.FileRevision{}).Where("node_id = ? AND container_id = ? AND path = ? AND state = ?", nodeID, id, file, "committed").Count(&baselineCount).Error; err != nil {
		return Content{}, err
	}
	if baselineCount == 0 {
		if err := s.insertRevision(ctx, nodeID, id, file, current.Content, userID, true, "committed"); err != nil {
			return Content{}, err
		}
	}
	newRow, err := s.prepareRevision(ctx, nodeID, id, file, value, userID, false, "pending")
	if err != nil {
		return Content{}, err
	}
	if err := s.write(ctx, runtime, id, current, value); err != nil {
		_ = s.db.Model(&newRow).Update("state", "failed").Error
		if errors.Is(err, ErrConflict) {
			return Content{}, err
		}
		if current.SingleFileBind {
			restoreCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, restoreErr := runtime.RunFileCommand(restoreCtx, id, writeScript, []string{current.Path}, []byte(current.Content), 1024)
			cancel()
			observedCtx, observedCancel := context.WithTimeout(context.Background(), 5*time.Second)
			observed, observeErr := s.Read(observedCtx, runtime, id, file)
			observedCancel()
			if restoreErr != nil || observeErr != nil || observed.ETag != current.ETag {
				return Content{}, fmt.Errorf("%w; original content could not be verified after restore; inspect the file", err)
			}
			return Content{}, fmt.Errorf("%w; original content restored and verified", err)
		}
		return Content{}, err
	}
	verified, err := s.Read(ctx, runtime, id, file)
	if err != nil || verified.ETag != hash(value) {
		_ = s.db.Model(&newRow).Update("state", "failed").Error
		if err != nil && current.SingleFileBind {
			restoreCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, restoreErr := runtime.RunFileCommand(restoreCtx, id, writeScript, []string{current.Path}, []byte(current.Content), 1024)
			cancel()
			observedCtx, observedCancel := context.WithTimeout(context.Background(), 5*time.Second)
			observed, observeErr := s.Read(observedCtx, runtime, id, file)
			observedCancel()
			if restoreErr == nil && observeErr == nil && observed.ETag == current.ETag {
				return Content{}, errors.New("saved content could not be verified; original content restored and verified")
			}
			return Content{}, errors.New("saved content could not be verified; original content could not be restored and verified; inspect the file")
		}
		if err != nil {
			return Content{}, fmt.Errorf("saved content could not be verified; inspect the file: %w", err)
		}
		return Content{}, fmt.Errorf("saved content differs from requested content (observed SHA-256 %s); inspect the file", verified.ETag)
	}
	if err := s.db.WithContext(ctx).Model(&newRow).Update("state", "committed").Error; err != nil {
		return Content{}, err
	}
	_ = s.prune(ctx, nodeID, id, file)
	return verified, nil
}

func (s *Service) prepareRevision(ctx context.Context, nodeID, id, file, value string, userID *uint, baseline bool, state string) (database.FileRevision, error) {
	ciphertext, err := s.secrets.Encrypt("v1:" + value)
	if err != nil {
		return database.FileRevision{}, err
	}
	row := database.FileRevision{NodeID: nodeID, ContainerID: id, Path: file, Hash: hash(value), Ciphertext: ciphertext, UserID: userID, Baseline: baseline, State: state}
	return row, s.db.WithContext(ctx).Create(&row).Error
}

func (s *Service) insertRevision(ctx context.Context, nodeID, id, file, value string, userID *uint, baseline bool, state string) error {
	_, err := s.prepareRevision(ctx, nodeID, id, file, value, userID, baseline, state)
	return err
}

func (s *Service) reconcile(ctx context.Context, runtime Runtime, nodeID, id, file string) error {
	var pending []database.FileRevision
	if err := s.db.WithContext(ctx).Where("node_id = ? AND container_id = ? AND path = ? AND state = ?", nodeID, id, file, "pending").Find(&pending).Error; err != nil {
		return err
	}
	if len(pending) == 0 {
		return nil
	}
	current, err := s.Read(ctx, runtime, id, file)
	if err != nil {
		return err
	}
	for _, row := range pending {
		state := "failed"
		if current.ETag == row.Hash {
			state = "committed"
		}
		if err := s.db.WithContext(ctx).Model(&row).Update("state", state).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) History(ctx context.Context, runtime Runtime, nodeID, id, file string) ([]Revision, error) {
	name, err := CleanPath(file)
	if err != nil {
		return nil, err
	}
	if err := s.reconcile(ctx, runtime, nodeID, id, name); err != nil {
		return nil, err
	}
	var rows []database.FileRevision
	if err := s.db.WithContext(ctx).Where("node_id = ? AND container_id = ? AND path = ? AND state = ?", nodeID, id, name, "committed").Order("id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]Revision, 0, len(rows))
	for _, row := range rows {
		result = append(result, Revision{ID: row.ID, Hash: row.Hash, UserID: row.UserID, CreatedAt: row.CreatedAt, Baseline: row.Baseline})
	}
	return result, nil
}

func (s *Service) RevisionContent(ctx context.Context, nodeID, id, file string, revisionID uint) (Content, error) {
	name, err := CleanPath(file)
	if err != nil {
		return Content{}, err
	}
	var row database.FileRevision
	if err := s.db.WithContext(ctx).Where("id = ? AND node_id = ? AND container_id = ? AND path = ? AND state = ?", revisionID, nodeID, id, name, "committed").First(&row).Error; err != nil {
		return Content{}, err
	}
	value, err := s.secrets.Decrypt(row.Ciphertext)
	if err != nil {
		return Content{}, err
	}
	if !strings.HasPrefix(value, "v1:") {
		return Content{}, errors.New("invalid file revision")
	}
	return Content{Path: name, Content: strings.TrimPrefix(value, "v1:"), ETag: row.Hash, Persistent: true}, nil
}

func (s *Service) Restore(ctx context.Context, runtime Runtime, nodeID, id, file string, revisionID uint, expected string, userID *uint) (Content, error) {
	prior, err := s.RevisionContent(ctx, nodeID, id, file, revisionID)
	if err != nil {
		return Content{}, err
	}
	return s.save(ctx, runtime, nodeID, id, file, expected, prior.Content, userID, true)
}

func (s *Service) prune(ctx context.Context, nodeID, id, file string) error {
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	if err := s.db.WithContext(ctx).Where("node_id = ? AND container_id = ? AND path = ? AND created_at < ?", nodeID, id, file, cutoff).Delete(&database.FileRevision{}).Error; err != nil {
		return err
	}
	var rows []database.FileRevision
	if err := s.db.WithContext(ctx).Where("node_id = ? AND container_id = ? AND path = ? AND state = ?", nodeID, id, file, "committed").Order("id DESC").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows[min(20, len(rows)):] {
		if err := s.db.WithContext(ctx).Delete(&row).Error; err != nil {
			return err
		}
	}
	var total int64
	if err := s.db.WithContext(ctx).Model(&database.FileRevision{}).Select("COALESCE(SUM(LENGTH(ciphertext)), 0)").Scan(&total).Error; err != nil {
		return err
	}
	if total <= historyBudget {
		return nil
	}
	var oldest []database.FileRevision
	if err := s.db.WithContext(ctx).Where("state = ?", "committed").Order("id ASC").Find(&oldest).Error; err != nil {
		return err
	}
	for _, row := range oldest {
		if total <= historyBudget {
			break
		}
		total -= int64(len(row.Ciphertext))
		if err := s.db.WithContext(ctx).Delete(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

// PruneAll enforces time, per-file, and total-size retention even for files
// that have not been opened recently.
func (s *Service) PruneAll(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	if err := s.db.WithContext(ctx).Where("created_at < ?", cutoff).Delete(&database.FileRevision{}).Error; err != nil {
		return err
	}
	type metadata struct {
		ID          uint
		NodeID      string
		ContainerID string
		Path        string
		Size        int64
	}
	var rows []metadata
	if err := s.db.WithContext(ctx).Model(&database.FileRevision{}).Select("id, node_id, container_id, path, LENGTH(ciphertext) AS size").Where("state = ?", "committed").Order("id DESC").Scan(&rows).Error; err != nil {
		return err
	}
	counts := map[string]int{}
	removed := map[uint]bool{}
	for _, row := range rows {
		key := row.NodeID + "\x00" + row.ContainerID + "\x00" + row.Path
		counts[key]++
		if counts[key] > 20 {
			if err := s.db.WithContext(ctx).Delete(&database.FileRevision{}, row.ID).Error; err != nil {
				return err
			}
			removed[row.ID] = true
		}
	}
	var total int64
	if err := s.db.WithContext(ctx).Model(&database.FileRevision{}).Select("COALESCE(SUM(LENGTH(ciphertext)), 0)").Scan(&total).Error; err != nil {
		return err
	}
	for i := len(rows) - 1; total > historyBudget && i >= 0; i-- {
		row := rows[i]
		if removed[row.ID] {
			continue
		}
		if err := s.db.WithContext(ctx).Delete(&database.FileRevision{}, row.ID).Error; err != nil {
			return err
		}
		total -= row.Size
	}
	return nil
}

func (s *Service) Apply(ctx context.Context, runtime Runtime, id string, input Action) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.apply(ctx, runtime, id, input)
}

// ApplyForNode keeps a file action and its revision-path update together with
// editor saves, so a concurrent save cannot attach history to the old path.
func (s *Service) ApplyForNode(ctx context.Context, runtime Runtime, nodeID, id string, input Action) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.apply(ctx, runtime, id, input); err != nil {
		return err
	}
	if err := s.AfterAction(ctx, nodeID, id, input); err != nil {
		return fmt.Errorf("container operation applied, but editor history could not be updated: %w", err)
	}
	return nil
}

func (s *Service) apply(ctx context.Context, runtime Runtime, id string, input Action) error {
	name, err := CleanPath(input.Path)
	if err != nil {
		return err
	}
	if name == "/" {
		return ErrUnsupported
	}
	row, err := s.inspect(ctx, runtime, id)
	if err != nil {
		return err
	}
	if err := guardPath(ctx, runtime, id, path.Dir(name)); err != nil {
		return err
	}
	all := mounts(row)
	for _, mount := range all {
		if (input.Action == "delete" || input.Action == "move" || input.Action == "rename" || input.Action == "copy") && (name == mount.Destination || strings.HasPrefix(mount.Destination, name+"/")) {
			return errors.New("cannot operate on a mount point or its parent")
		}
	}
	if mount := effectiveMount(all, name); (mount != nil && !mount.ReadWrite) || (mount == nil && row.ReadOnlyRootFS) {
		return ErrUnsupported
	}
	var script string
	args := []string{name}
	switch input.Action {
	case "create_file":
		script = `[ ! -e "$1" ] && [ ! -L "$1" ] || { echo 'target already exists' >&2; exit 2; }; set -C; : > "$1"`
	case "create_directory":
		script = `[ ! -e "$1" ] && [ ! -L "$1" ] || { echo 'target already exists' >&2; exit 2; }; mkdir -- "$1"`
	case "delete":
		if err := guardPath(ctx, runtime, id, name); err != nil {
			return err
		}
		script = `[ -e "$1" ] || exit 2; if [ -d "$1" ]; then rm -r -- "$1"; else rm -f -- "$1"; fi`
	case "rename", "copy", "move":
		target, err := CleanPath(input.Target)
		if err != nil {
			return err
		}
		if target == "/" || target == name || strings.HasPrefix(target, name+"/") {
			return ErrUnsupported
		}
		if input.Action == "rename" && path.Dir(target) != path.Dir(name) {
			return errors.New("rename must stay in the same directory")
		}
		if err := guardPath(ctx, runtime, id, name); err != nil {
			return err
		}
		if err := guardPath(ctx, runtime, id, path.Dir(target)); err != nil {
			return err
		}
		if mount := effectiveMount(all, target); (mount != nil && !mount.ReadWrite) || (mount == nil && row.ReadOnlyRootFS) {
			return ErrUnsupported
		}
		args = append(args, target)
		switch input.Action {
		case "rename", "move":
			fromMount, toMount := effectiveMount(all, name), effectiveMount(all, target)
			if input.Action == "rename" || (fromMount == nil && toMount == nil) || (fromMount != nil && toMount != nil && fromMount.Destination == toMount.Destination) {
				script = `[ -e "$1" ] || { echo 'source not found' >&2; exit 2; }; [ ! -e "$2" ] && [ ! -L "$2" ] || { echo 'target already exists' >&2; exit 2; }; mv -n -- "$1" "$2" && [ ! -e "$1" ]`
			} else {
				stage := path.Join(path.Dir(target), fmt.Sprintf(".suma-move-%d", time.Now().UnixNano()))
				args = append(args, stage)
				script = `[ -e "$1" ] || { echo 'source not found' >&2; exit 2; }; [ ! -e "$2" ] && [ ! -L "$2" ] && [ ! -e "$3" ] && [ ! -L "$3" ] || { echo 'target already exists' >&2; exit 2; }; cp -R -p -- "$1" "$3" || { rm -r -f -- "$3"; exit 1; }; [ ! -e "$2" ] && mv -n -- "$3" "$2" && [ ! -e "$3" ] || { rm -r -f -- "$3"; echo 'target already exists' >&2; exit 2; }; rm -r -- "$1"`
			}
		case "copy":
			stage := path.Join(path.Dir(target), fmt.Sprintf(".suma-copy-%d", time.Now().UnixNano()))
			args = append(args, stage)
			script = `[ -e "$1" ] || { echo 'source not found' >&2; exit 2; }; [ ! -e "$2" ] && [ ! -L "$2" ] && [ ! -e "$3" ] && [ ! -L "$3" ] || { echo 'target already exists' >&2; exit 2; }; cp -R -p -- "$1" "$3" || { rm -r -f -- "$3"; exit 1; }; [ ! -e "$2" ] && mv -n -- "$3" "$2" && [ ! -e "$3" ] || { rm -r -f -- "$3"; echo 'target already exists' >&2; exit 2; }`
		}
	default:
		return ErrUnsupported
	}
	if input.Action == "delete" || input.Action == "rename" || input.Action == "copy" || input.Action == "move" {
		script = `[ -e "$1" ] || { echo 'source not found' >&2; exit 2; }; [ -f "$1" ] || [ -d "$1" ] || { echo 'special files cannot be modified' >&2; exit 70; }; ` + script
	}
	_, err = runtime.RunFileCommand(ctx, id, script, args, nil, 4096)
	if err != nil && strings.Contains(err.Error(), "target already exists") {
		return fmt.Errorf("%w: target already exists", ErrConflict)
	}
	return err
}

// AfterAction follows successful moves/renames and prevents a new file at a
// deleted path from inheriting an unrelated editor history.
func (s *Service) AfterAction(ctx context.Context, nodeID, id string, input Action) error {
	if input.Action != "delete" && input.Action != "rename" && input.Action != "move" {
		return nil
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []database.FileRevision
		if err := tx.Where("node_id = ? AND container_id = ?", nodeID, id).Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			if row.Path != input.Path && !strings.HasPrefix(row.Path, input.Path+"/") {
				continue
			}
			if input.Action == "delete" {
				if err := tx.Delete(&row).Error; err != nil {
					return err
				}
			} else {
				next := input.Target + strings.TrimPrefix(row.Path, input.Path)
				if err := tx.Model(&row).Update("path", next).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}
