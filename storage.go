package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func initGlobalStorage() {
	os.MkdirAll(GlobalBlobsDir, 0755)
	os.MkdirAll(GlobalProjectsDir, 0755)
}

func initLocalStorage() {
	os.MkdirAll(LocalRefsDir, 0755)
	os.MkdirAll(LocalBranchesDir, 0755)
	if _, err := os.Stat(LocalHeadFile); os.IsNotExist(err) {
		setHead("main")
		setBranch("main", "")
	}
}

func getHead() string {
	b, err := os.ReadFile(LocalHeadFile)
	if err != nil {
		return "main"
	}
	return strings.TrimSpace(string(b))
}

func setHead(target string) {
	os.WriteFile(LocalHeadFile, []byte(target), 0644)
}

func getBranch(name string) string {
	path := filepath.Join(LocalBranchesDir, name)
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func setBranch(name, commitID string) {
	os.MkdirAll(LocalBranchesDir, 0755)
	path := filepath.Join(LocalBranchesDir, name)
	os.WriteFile(path, []byte(commitID), 0644)
}

func atomicWriteJSON(path string, data interface{}) error {
	tmpPath := path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(data); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return err
	}
	f.Close()
	return os.Rename(tmpPath, path)
}

func copyFileAtomic(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	tmpDst := dst + ".tmp"
	destFile, err := os.Create(tmpDst)
	if err != nil {
		return err
	}

	if _, err = io.Copy(destFile, sourceFile); err != nil {
		destFile.Close()
		os.Remove(tmpDst)
		return err
	}
	destFile.Close()
	return os.Rename(tmpDst, dst)
}

func loadConfig() RepoConfig {
	var config RepoConfig

	// 1. Load Global Config (Defaults)
	gFile, err := os.Open(GlobalConfigPath)
	if err == nil {
		json.NewDecoder(gFile).Decode(&config)
		gFile.Close()
	}

	// 2. Load Local Config (Overrides)
	lFile, err := os.Open(LocalConfig)
	if err == nil {
		var lConfig RepoConfig
		json.NewDecoder(lFile).Decode(&lConfig)
		lFile.Close()

		// Merge
		if lConfig.Name != "" {
			config.Name = lConfig.Name
		}
		if lConfig.RemoteURL != "" {
			config.RemoteURL = lConfig.RemoteURL
		}
		if lConfig.RemoteUser != "" {
			config.RemoteUser = lConfig.RemoteUser
		}
		if lConfig.Author != "" {
			config.Author = lConfig.Author
		}
		config.Sync = lConfig.Sync
		config.CurrentBranch = lConfig.CurrentBranch
		config.RemoteHash = lConfig.RemoteHash
	}

	if config.Name == "" {
		wd, _ := os.Getwd()
		config.Name = filepath.Base(wd)
	}
	if config.Author == "" {
		config.Author = config.RemoteUser
	}

	return config
}

func saveConfig(config RepoConfig) {
	// If inside a repo, save locally
	if _, err := os.Stat(LocalRepoDir); err == nil {
		atomicWriteJSON(LocalConfig, config)
	}

	// Always update global identity
	os.MkdirAll(GlobalFalconDir, 0755)
	globalConf := RepoConfig{
		RemoteURL:  config.RemoteURL,
		RemoteUser: config.RemoteUser,
		Author:     config.Author,
		RemoteHash: config.RemoteHash,
	}
	atomicWriteJSON(GlobalConfigPath, globalConf)
}

func loadManifest(idOrVersion string) (*VersionManifest, error) {
	// Try direct CommitID lookup first
	path := filepath.Join(LocalRefsDir, idOrVersion+".json")
	if _, err := os.Stat(path); err == nil {
		return loadManifestFromFile(path)
	}

	// Then try branch lookup
	if commitID := getBranch(idOrVersion); commitID != "" {
		return loadManifest(commitID)
	}

	// Then try scanning for Version field
	entries, _ := os.ReadDir(LocalRefsDir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			m, err := loadManifestFromFile(filepath.Join(LocalRefsDir, e.Name()))
			if err == nil && m.Version == idOrVersion {
				return m, nil
			}
		}
	}

	return nil, fmt.Errorf("manifest not found: %s", idOrVersion)
}

func loadManifestFromFile(path string) (*VersionManifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var m VersionManifest
	err = json.NewDecoder(file).Decode(&m)
	return &m, err
}

func calculateCommitID(m VersionManifest) string {
	// Exclude CommitID itself from hashing
	m.CommitID = ""
	data, _ := json.Marshal(m)
	hasher := sha256.New()
	hasher.Write(data)
	return hex.EncodeToString(hasher.Sum(nil))[:16] // Short hash like Git
}

func calculateFileHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func copyDir(src, dst string) {
	entries, _ := os.ReadDir(src)
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if !entry.IsDir() {
			copyFileAtomic(srcPath, dstPath)
		}
	}
}

func loadIndex() map[string]IndexEntry {
	idx := make(map[string]IndexEntry)
	if b, err := os.ReadFile(LocalIndex); err == nil {
		json.Unmarshal(b, &idx)
	}
	return idx
}

func saveIndex(idx map[string]IndexEntry) {
	atomicWriteJSON(LocalIndex, idx)
}

func loadIgnorePatterns() ([]string, error) {
	file, err := os.Open(IgnoreFile)
	if os.IsNotExist(err) {
		return nil, nil
	}
	defer file.Close()
	var patterns []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			patterns = append(patterns, line)
		}
	}
	return patterns, nil
}

func shouldIgnore(path string, info os.FileInfo, patterns []string) bool {
	pathSlash := filepath.ToSlash(filepath.Clean(path))

	if strings.HasPrefix(pathSlash, LocalRepoDir) || strings.Contains(pathSlash, ".git") {
		return true
	}
	if info.Name() == "main.go" || info.Name() == "main.exe" || info.Name() == "main" || info.Name() == "falcon.exe" || info.Name() == "falcon" {
		return true // Self executable protection
	}

	for _, pattern := range patterns {
		cleanPattern := strings.TrimSuffix(pattern, "/")

		// Simple recursive pattern heuristic (Treat `**` as standard prefix for now in custom logic)
		if strings.Contains(cleanPattern, "**") {
			cleanPattern = strings.ReplaceAll(cleanPattern, "**", "*")
		}

		if strings.Contains(cleanPattern, "/") {
			cleanPattern = strings.TrimPrefix(cleanPattern, "/")
			matched, err := filepath.Match(cleanPattern, pathSlash)
			// Secondary prefix matching logic for paths (e.g. node_modules/ ignoring everything inside)
			if (err == nil && matched) || strings.HasPrefix(pathSlash+"/", cleanPattern+"/") {
				return true
			}
		} else {
			matched, err := filepath.Match(cleanPattern, info.Name())
			if err == nil && matched {
				return true
			}
		}
	}
	return false
}

func updateIndexForFile(path string, staged bool) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	hash, err := calculateFileHash(path)
	if err != nil {
		return err
	}

	index := loadIndex()
	entry, exists := index[path]
	if !exists {
		entry = IndexEntry{}
	}

	entry.Hash = hash
	entry.Size = info.Size()
	entry.ModTime = info.ModTime()

	if staged {
		entry.Staged = true
		entry.StagedHash = hash

		// Ensure blob exists in global storage
		blobPath := filepath.Join(GlobalBlobsDir, hash)
		if _, err := os.Stat(blobPath); os.IsNotExist(err) {
			copyFileAtomic(path, blobPath)
		}
	} else {
		// Just sync metadata if not staging
		// Entry.Hash is already updated above
	}

	index[path] = entry
	saveIndex(index)
	return nil
}

func clearStaging(paths []string) {
	index := loadIndex()
	if paths == nil {
		for p := range index {
			entry := index[p]
			entry.Staged = false
			index[p] = entry
		}
	} else {
		for _, p := range paths {
			if entry, ok := index[p]; ok {
				entry.Staged = false
				index[p] = entry
			}
		}
	}
	saveIndex(index)
}

func readBlob(hash string) ([]byte, error) {
	path := filepath.Join(GlobalBlobsDir, hash)
	return os.ReadFile(path)
}

func isBinary(data []byte) bool {
	// Simple heuristic: check for null bytes in the first 1KB
	limit := len(data)
	if limit > 1024 {
		limit = 1024
	}
	for i := 0; i < limit; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}
