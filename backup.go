package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Manifest describes a backup archive. It is written next to the archive
// with mode 0600, because it contains the user's shadow entries.
type Manifest struct {
	Username     string              `json:"username"`
	UID          int                 `json:"uid"`
	GID          int                 `json:"gid"`
	HomeDir      string              `json:"home_dir"`
	Shell        string              `json:"shell"`
	Created      time.Time           `json:"created"`
	Hostname     string              `json:"hostname"`
	Archive      string              `json:"archive"`
	SHA256       string              `json:"sha256"`
	Configs      []string            `json:"configs"`
	AccountLines map[string][]string `json:"account_lines"`
	Files        []ManifestFile      `json:"files"`
	Skipped      []string            `json:"skipped,omitempty"`
}

// ManifestFile is one entry of the archive
type ManifestFile struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Mode string `json:"mode"`
	UID  int    `json:"uid"`
	GID  int    `json:"gid"`
	Size int64  `json:"size"`
}

// backupTimeFormat is used in archive names and sorts chronologically
const backupTimeFormat = "20060102-150405"

// checkBackupDir makes sure the backup directory is absolute and outside
// of the home of any deletable user, so removing a home can never remove
// a backup.
func checkBackupDir(dir string, users map[string]*User) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("backup directory %q must be an absolute path", dir)
	}
	dir = filepath.Clean(dir)
	inside := func(parent string) bool {
		parent = filepath.Clean(parent)
		return parent != "/" && (dir == parent || strings.HasPrefix(dir, parent+"/"))
	}
	if inside(homeDir) {
		return fmt.Errorf("backup directory %s must not be inside %s", dir, homeDir)
	}
	for _, user := range users {
		// System users like "backup" (home /var/backups) are never deleted
		if isProtected(user) == "" && inside(user.HomeDir) {
			return fmt.Errorf("backup directory %s must not be inside the home of %s (%s)", dir, user.Username, user.HomeDir)
		}
	}
	return nil
}

// backupSources returns the paths to back up for a user: home directory,
// config files, crontab and mail spool, without duplicates.
func backupSources(user *User) []string {
	seen := make(map[string]bool)
	var sources []string
	add := func(path string) {
		if path == "" || seen[path] {
			return
		}
		if _, err := os.Lstat(path); err != nil {
			return
		}
		seen[path] = true
		sources = append(sources, path)
	}

	if home := filepath.Clean(user.HomeDir); home != "/" {
		add(home)
	}
	for _, config := range user.Configs {
		add(config)
	}
	add(filepath.Join(cronDir, user.Username))
	add(filepath.Join(mailDir, user.Username))
	return sources
}

// backupUser writes <user>-<time>.tar.gz, a .manifest.json and a .sha256
// file into the backup directory and returns the archive path.
// Owners, modes and timestamps are preserved in the archive.
func backupUser(user *User, users map[string]*User, dir string) (string, error) {
	if err := checkBackupDir(dir, users); err != nil {
		return "", err
	}

	stamp := time.Now().Format(backupTimeFormat)
	base := fmt.Sprintf("%s-%s", user.Username, stamp)
	archivePath := filepath.Join(dir, base+".tar.gz")
	sources := backupSources(user)

	if dryRun {
		fmt.Printf("Would write %s with:\n", archivePath)
		for _, src := range sources {
			fmt.Printf("  - %s\n", src)
		}
		return archivePath, nil
	}

	if requireRoot && os.Geteuid() != 0 {
		return "", fmt.Errorf("backup must run as root to read all files of %s", user.Username)
	}

	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return "", err
	}
	if _, err := os.Stat(archivePath); err == nil {
		return "", fmt.Errorf("backup %s already exists", archivePath)
	}

	tmp, err := os.CreateTemp(dir, "."+base+"-*.tmp")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name()) // no-op after successful rename

	hostname, _ := os.Hostname()
	manifest := &Manifest{
		Username:     user.Username,
		UID:          user.UID,
		GID:          user.GID,
		HomeDir:      user.HomeDir,
		Shell:        user.Shell,
		Created:      time.Now(),
		Hostname:     hostname,
		Archive:      filepath.Base(archivePath),
		Configs:      user.Configs,
		AccountLines: accountLines(user.Username),
	}

	hash := sha256.New()
	gz := gzip.NewWriter(io.MultiWriter(tmp, hash))
	tw := tar.NewWriter(gz)
	for _, src := range sources {
		if err := addToArchive(tw, src, manifest); err != nil {
			tmp.Close()
			return "", err
		}
	}
	if err := tw.Close(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := gz.Close(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), archivePath); err != nil {
		return "", err
	}

	manifest.SHA256 = hex.EncodeToString(hash.Sum(nil))
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, base+".manifest.json"), append(data, '\n'), 0600); err != nil {
		return "", err
	}
	checksum := fmt.Sprintf("%s  %s\n", manifest.SHA256, manifest.Archive)
	if err := os.WriteFile(filepath.Join(dir, base+".sha256"), []byte(checksum), 0600); err != nil {
		return "", err
	}

	for _, skipped := range manifest.Skipped {
		fmt.Printf("Warning: not backed up: %s\n", skipped)
	}
	return archivePath, nil
}

// addToArchive adds a path, recursively for directories, to the archive.
// Symlinks are stored as links and never followed. Entries that cannot be
// read or are no regular file, directory or symlink are recorded as skipped.
func addToArchive(tw *tar.Writer, root string, manifest *Manifest) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			manifest.Skipped = append(manifest.Skipped, fmt.Sprintf("%s (%v)", path, err))
			return nil
		}

		var link, kind string
		switch {
		case info.Mode().IsRegular():
			kind = "file"
		case info.IsDir():
			kind = "dir"
		case info.Mode()&os.ModeSymlink != 0:
			kind = "symlink"
			if link, err = os.Readlink(path); err != nil {
				manifest.Skipped = append(manifest.Skipped, fmt.Sprintf("%s (%v)", path, err))
				return nil
			}
		default:
			manifest.Skipped = append(manifest.Skipped, fmt.Sprintf("%s (unsupported type %s)", path, info.Mode().Type()))
			return nil
		}

		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			manifest.Skipped = append(manifest.Skipped, fmt.Sprintf("%s (%v)", path, err))
			return nil
		}
		// Absolute paths without the leading slash, so that
		// "tar -xzpf <archive> -C /" restores to the original location.
		header.Name = strings.TrimPrefix(path, "/")
		if info.IsDir() {
			header.Name += "/"
		}

		var file *os.File
		if kind == "file" {
			if file, err = os.Open(path); err != nil {
				manifest.Skipped = append(manifest.Skipped, fmt.Sprintf("%s (%v)", path, err))
				return nil
			}
			defer file.Close()
		}

		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if file != nil {
			if _, err := io.CopyN(tw, file, header.Size); err != nil {
				return fmt.Errorf("writing %s: %w", path, err)
			}
		}

		manifest.Files = append(manifest.Files, ManifestFile{
			Path: path,
			Type: kind,
			Mode: fmt.Sprintf("%04o", info.Mode().Perm()),
			UID:  header.Uid,
			GID:  header.Gid,
			Size: header.Size,
		})
		return nil
	})
}

// latestBackup returns the newest archive of a user in the backup directory
func latestBackup(username, dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	re := regexp.MustCompile(`^` + regexp.QuoteMeta(username) + `-\d{8}-\d{6}\.tar\.gz$`)
	var names []string
	for _, entry := range entries {
		if entry.Type().IsRegular() && re.MatchString(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	if len(names) == 0 {
		return "", nil
	}
	sort.Strings(names)
	return filepath.Join(dir, names[len(names)-1]), nil
}

// readManifest reads the manifest belonging to an archive
func readManifest(archivePath string) (*Manifest, error) {
	data, err := os.ReadFile(strings.TrimSuffix(archivePath, ".tar.gz") + ".manifest.json")
	if err != nil {
		return nil, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}
