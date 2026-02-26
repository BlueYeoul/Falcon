package main

import (
	"os"
	"path/filepath"
	"time"
)

// --- [Configuration] ---

var (
	HomeDir, _        = os.UserHomeDir()
	GlobalCacheDir    = filepath.Join(HomeDir, ".cache", "falcon_cache")
	GlobalBlobsDir    = filepath.Join(GlobalCacheDir, "blobs")
	GlobalProjectsDir = filepath.Join(GlobalCacheDir, "projects")

	LocalRepoDir     = ".falcon"
	LocalRefsDir     = filepath.Join(LocalRepoDir, "refs")
	LocalBranchesDir = filepath.Join(LocalRepoDir, "branches")
	LocalHeadFile    = filepath.Join(LocalRepoDir, "HEAD")
	LocalConfig      = filepath.Join(LocalRepoDir, "config.json")
	LocalIndex       = filepath.Join(LocalRepoDir, "index.json") // Performance: caches modtime/size -> hash
	LocalLockFile    = filepath.Join(LocalRepoDir, "lock")
	IgnoreFile       = ".fignore"

	// 🌐 Global Identity Configuration
	GlobalFalconDir  = filepath.Join(HomeDir, ".falcon")
	GlobalConfigPath = filepath.Join(GlobalFalconDir, "config.json")

	// Server-side defaults
	ServerBaseDir     = "/tmp/falcon_server" // Default for demo/local testing
	ServerBlobsDir    = filepath.Join(ServerBaseDir, "blobs")
	ServerRefsDir     = filepath.Join(ServerBaseDir, "refs")
	ServerKeysDir     = filepath.Join(ServerBaseDir, "keys")
	ServerProjectsDir = filepath.Join(ServerBaseDir, "projects")

	// Local Authentication
	LocalKeyDir         = filepath.Join(HomeDir, ".falcon_keys")
	LocalPrivateKeyFile = filepath.Join(LocalKeyDir, "id_ed25519")
)

// Sync Modes
const (
	SyncNone   = "none"
	SyncMaster = "master"
	SyncSlave  = "slave"
)

// --- [Data Structures] ---

type SyncStatus struct {
	Mode         string `json:"mode"`
	TargetBranch string `json:"target_branch"`
	RemoteURL    string `json:"remote_url"`
}

type SyncSession struct {
	ID        string    `json:"id"`
	Project   string    `json:"project"`
	Branch    string    `json:"branch"`
	MasterID  string    `json:"master_id"`
	Slaves    []string  `json:"slaves"`
	CreatedAt time.Time `json:"created_at"`
}

type FileMeta struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
	Size int64  `json:"size"`
}

type VersionManifest struct {
	CommitID    string     `json:"commit_id"` // Hash of manifest content (excluding itself)
	Version     string     `json:"version"`   // Tag or semantic version (optional)
	Parents     []string   `json:"parents"`   // Parent Commit IDs
	CreatedAt   time.Time  `json:"created_at"`
	Author      string     `json:"author"`
	Description string     `json:"description"`
	Files       []FileMeta `json:"files"`
}

type RepoConfig struct {
	Name          string     `json:"name"`
	Author        string     `json:"author"`
	RemoteURL     string     `json:"remote_url"`
	RemoteHash    string     `json:"remote_hash"`
	RemoteUser    string     `json:"remote_user"`
	CurrentBranch string     `json:"current_branch"`
	Sync          SyncStatus `json:"sync"`
}

// IndexEntry caches file state to prevent slow full-scans
type IndexEntry struct {
	Hash       string    `json:"hash"`
	Size       int64     `json:"size"`
	ModTime    time.Time `json:"mod_time"`
	Staged     bool      `json:"staged"`
	StagedHash string    `json:"staged_hash"` // The hash of the file when it was 'added'
}
