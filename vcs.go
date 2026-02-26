package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// --- [Lock Management] ---

func acquireLock() func() {
	if _, err := os.Stat(LocalRepoDir); os.IsNotExist(err) {
		return func() {} // Not a repo yet
	}
	if _, err := os.Stat(LocalLockFile); err == nil {
		fmt.Println("[Error] Repository is locked by another process.")
		fmt.Println("If you are sure no other falcon process is running, use 'falcon unlock'.")
		os.Exit(1)
	}
	os.WriteFile(LocalLockFile, []byte(fmt.Sprintf("%d", os.Getpid())), 0644)
	return func() {
		os.Remove(LocalLockFile)
	}
}

// --- [VCS Logic] ---

func handleInit(name string) {
	targetDir := "."
	if name != "" {
		targetDir = name
		os.MkdirAll(name, 0755)
	}

	falconDir := filepath.Join(targetDir, LocalRepoDir)
	if _, err := os.Stat(falconDir); !os.IsNotExist(err) {
		fmt.Println("Falcon repository already initialized.")
		return
	}

	initLocalStorage()

	repoName := filepath.Base(targetDir)
	if name == "" {
		wd, _ := os.Getwd()
		repoName = filepath.Base(wd)
	}

	config := RepoConfig{Name: repoName, CurrentBranch: "main"}
	atomicWriteJSON(filepath.Join(targetDir, LocalConfig), config)

	fmt.Printf("Initialized Falcon repo '%s' in %s\n", repoName, targetDir)
}

func handleAdd(patterns []string) {
	ignorePatterns, _ := loadIgnorePatterns()
	count := 0

	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if shouldIgnore(path, info, ignorePatterns) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}

		matched := false
		for _, p := range patterns {
			if p == "." || strings.HasPrefix(path, p) || path == p {
				matched = true
				break
			}
		}

		if matched {
			updateIndexForFile(path, true)
			count++
		}
		return nil
	})

	if err == nil {
		fmt.Printf("[Falcon] Staged %d files.\n", count)
	}
}

func handleStatus() {
	index := loadIndex()
	ignorePatterns, _ := loadIgnorePatterns()
	head := getHead()
	headCommitID := getBranch(head)
	if headCommitID == "" {
		headCommitID = head
	}

	var headFiles map[string]FileMeta
	if headCommitID != "" {
		m, err := loadManifest(headCommitID)
		if err == nil {
			headFiles = toFileMap(m.Files)
		}
	} else {
		headFiles = make(map[string]FileMeta)
	}

	staged := []string{}
	modified := []string{}
	untracked := []string{}

	// 1. Working Dir vs Index
	filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if shouldIgnore(path, info, ignorePatterns) {
			return nil
		}

		entry, exists := index[path]
		if !exists {
			untracked = append(untracked, path)
		} else {
			if entry.Size != info.Size() || !entry.ModTime.Equal(info.ModTime()) {
				h, _ := calculateFileHash(path)
				if h != entry.Hash {
					modified = append(modified, path)
				}
			}
		}
		return nil
	})

	// 2. Index vs HEAD
	for path, entry := range index {
		if entry.Staged {
			headFile, inHead := headFiles[path]
			if !inHead || headFile.Hash != entry.StagedHash {
				staged = append(staged, path)
			}
		}
	}

	fmt.Printf("On branch \033[36m%s\033[0m\n", head)

	if len(staged) > 0 {
		fmt.Println("\nChanges to be committed:")
		for _, f := range staged {
			fmt.Printf("  (staged)   \033[32m%s\033[0m\n", f)
		}
	}

	if len(modified) > 0 {
		fmt.Println("\nChanges not staged for commit:")
		for _, f := range modified {
			fmt.Printf("  (modified) \033[31m%s\033[0m\n", f)
		}
	}

	if len(untracked) > 0 {
		fmt.Println("\nUntracked files:")
		for _, f := range untracked {
			fmt.Printf("  \033[30;1m%s\033[0m\n", f)
		}
	}

	if len(staged) == 0 && len(modified) == 0 && len(untracked) == 0 {
		fmt.Println("nothing to commit, working tree clean")
	}
}

func handleCommit(isMajor, isMinor bool, description string) {
	defer acquireLock()()
	config := loadConfig()
	index := loadIndex()
	headTarget := getHead()

	headCommitID := getBranch(headTarget)
	if headCommitID == "" {
		headCommitID = headTarget
	}

	currentFiles := make(map[string]FileMeta)
	if headCommitID != "" {
		m, err := loadManifest(headCommitID)
		if err == nil {
			currentFiles = toFileMap(m.Files)
		}
	}

	stagedPaths := []string{}
	for path, entry := range index {
		if entry.Staged {
			currentFiles[path] = FileMeta{Path: path, Hash: entry.StagedHash, Size: entry.Size}
			stagedPaths = append(stagedPaths, path)
		}
	}

	if len(stagedPaths) == 0 {
		fmt.Println("nothing staged for commit")
		return
	}

	commitFiles := []FileMeta{}
	for _, f := range currentFiles {
		commitFiles = append(commitFiles, f)
	}

	parents := []string{}
	if headCommitID != "" {
		parents = append(parents, headCommitID)
	}

	version := ""
	if isMajor || isMinor {
		version = getNextVersion(isMajor, isMinor)
	}

	manifest := VersionManifest{
		Version:     version,
		Parents:     parents,
		CreatedAt:   time.Now(),
		Author:      config.Author,
		Description: description,
		Files:       commitFiles,
	}

	commitID := calculateCommitID(manifest)
	manifest.CommitID = commitID
	atomicWriteJSON(filepath.Join(LocalRefsDir, commitID+".json"), manifest)

	if getBranch(headTarget) != "" || headTarget == "main" {
		setBranch(headTarget, commitID)
	} else {
		setHead(commitID)
	}

	clearStaging(stagedPaths)
	fmt.Printf("[%s %s] %s\n", headTarget, commitID[:7], description)
	fmt.Printf(" %d files changed\n", len(stagedPaths))

	// Sync to global
	globalRefDir := filepath.Join(GlobalProjectsDir, config.Name, "refs")
	os.MkdirAll(globalRefDir, 0755)
	atomicWriteJSON(filepath.Join(globalRefDir, commitID+".json"), manifest)
}

func handleBranchList() {
	entries, _ := os.ReadDir(LocalBranchesDir)
	head := getHead()

	fmt.Println("\n🌿 Branches")
	for _, e := range entries {
		prefix := "  "
		if e.Name() == head {
			prefix = "* "
		}
		commitID := getBranch(e.Name())
		fmt.Printf("%s%s (%s)\n", prefix, e.Name(), commitID)
	}
}

func handleBranchCreate(name string) {
	if getBranch(name) != "" {
		fmt.Printf("Branch '%s' already exists.\n", name)
		return
	}
	head := getHead()
	commitID := ""
	if cid := getBranch(head); cid != "" {
		commitID = cid
	} else {
		commitID = head
	}

	setBranch(name, commitID)
	fmt.Printf("Created branch '%s' at %s\n", name, commitID)
}

func handleCheckout(target string) {
	defer acquireLock()()

	manifest, err := loadManifest(target)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("[Falcon] Checking out '%s' (%s)...\n", target, manifest.CommitID)
	restoreFiles(manifest, true)

	setHead(target)
	fmt.Printf("Switched to %s\n", target)
}

func handleMerge(branchName string) {
	defer acquireLock()()

	currentBranch := getHead()
	currentCommitID := getBranch(currentBranch)
	if currentCommitID == "" {
		fmt.Println("Error: Not on a branch (Detached HEAD). Merge aborted.")
		return
	}

	targetCommitID := getBranch(branchName)
	if targetCommitID == "" {
		fmt.Printf("Error: Branch '%s' not found.\n", branchName)
		return
	}

	if currentCommitID == targetCommitID {
		fmt.Println("Already up to date.")
		return
	}

	// 1. Find Common Ancestor
	ancestorCommitID := findCommonAncestor(currentCommitID, targetCommitID)
	if ancestorCommitID == "" {
		fmt.Println("Warning: No common ancestor found. Performing root merge.")
	}

	mCurrent, _ := loadManifest(currentCommitID)
	mTarget, _ := loadManifest(targetCommitID)
	var mBase *VersionManifest
	if ancestorCommitID != "" {
		mBase, _ = loadManifest(ancestorCommitID)
	} else {
		mBase = &VersionManifest{} // Empty base
	}

	// 2. Three-way Merge Logic
	mergedFiles, conflicts := mergeFileMaps(mBase.Files, mCurrent.Files, mTarget.Files)

	if len(conflicts) > 0 {
		fmt.Printf("\n❌ [Merge Conflict] %d files conflict:\n", len(conflicts))
		for _, c := range conflicts {
			fmt.Printf("  ! %s\n", c)
		}
		fmt.Println("\nMerge aborted. Solve conflicts manually or use force (not implemented).")
		return
	}

	// 3. Create Merge Commit
	fmt.Printf("[Falcon] Merging '%s' into '%s'...\n", branchName, currentBranch)

	manifest := VersionManifest{
		Version:     "",
		Parents:     []string{currentCommitID, targetCommitID},
		CreatedAt:   time.Now(),
		Description: fmt.Sprintf("Merge branch '%s' into '%s'", branchName, currentBranch),
		Files:       mergedFiles,
	}

	commitID := calculateCommitID(manifest)
	manifest.CommitID = commitID
	atomicWriteJSON(filepath.Join(LocalRefsDir, commitID+".json"), manifest)

	setBranch(currentBranch, commitID)
	restoreFiles(&manifest, true)

	fmt.Printf("✨ Merge complete: %s\n", commitID)
}

func findCommonAncestor(c1, c2 string) string {
	// Simple BFS to find first common ancestor
	visited := make(map[string]bool)
	queue := []string{c1}

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		if curr == "" {
			continue
		}
		visited[curr] = true

		m, err := loadManifest(curr)
		if err == nil {
			queue = append(queue, m.Parents...)
		}
	}

	queue = []string{c2}
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		if curr == "" {
			continue
		}
		if visited[curr] {
			return curr
		}
		m, err := loadManifest(curr)
		if err == nil {
			queue = append(queue, m.Parents...)
		}
	}
	return ""
}

func mergeFileMaps(base, current, target []FileMeta) ([]FileMeta, []string) {
	baseMap := toFileMap(base)
	currentMap := toFileMap(current)
	targetMap := toFileMap(target)

	merged := make(map[string]FileMeta)
	var conflicts []string

	// Collect all unique paths
	allPaths := make(map[string]bool)
	for p := range baseMap {
		allPaths[p] = true
	}
	for p := range currentMap {
		allPaths[p] = true
	}
	for p := range targetMap {
		allPaths[p] = true
	}

	for path := range allPaths {
		b, inB := baseMap[path]
		c, inC := currentMap[path]
		t, inT := targetMap[path]

		if !inB {
			// Case 1: Added in both
			if inC && inT {
				if c.Hash == t.Hash {
					merged[path] = c
				} else {
					conflicts = append(conflicts, path)
				}
			} else if inC {
				merged[path] = c
			} else if inT {
				merged[path] = t
			}
		} else {
			// Case 2: Exists in base
			changedC := inC && c.Hash != b.Hash
			changedT := inT && t.Hash != b.Hash
			deletedC := !inC
			deletedT := !inT

			if !changedC && !changedT && !deletedC && !deletedT {
				merged[path] = b
			} else if changedC && !changedT && inT {
				merged[path] = c
			} else if !changedC && changedT && inC {
				merged[path] = t
			} else if changedC && changedT {
				if c.Hash == t.Hash {
					merged[path] = c
				} else {
					conflicts = append(conflicts, path)
				}
			} else if deletedC && !changedT {
				// OK: deleted in C, no change in T -> stays deleted
			} else if deletedT && !changedC {
				// OK: deleted in T, no change in C -> stays deleted
			} else {
				conflicts = append(conflicts, path)
			}
		}
	}

	var result []FileMeta
	for _, m := range merged {
		result = append(result, m)
	}
	return result, conflicts
}

func toFileMap(files []FileMeta) map[string]FileMeta {
	m := make(map[string]FileMeta)
	for _, f := range files {
		m[f.Path] = f
	}
	return m
}

func handleLog() {
	head := getHead()
	curr := getBranch(head)
	if curr == "" {
		curr = head
	}

	fmt.Println("\n📜 Commit History")
	fmt.Println("---------------------------------------------------------------")

	visited := make(map[string]bool)
	for curr != "" && !visited[curr] {
		m, err := loadManifest(curr)
		if err != nil {
			break
		}
		visited[curr] = true

		dateStr := m.CreatedAt.Format("2006-01-02 15:04")
		pref := "* "
		if m.CommitID == getBranch(head) {
			pref = "● "
		}

		fmt.Printf("%s\033[33m%s\033[0m (%s) - %s\n", pref, m.CommitID[:7], dateStr, m.Description)
		fmt.Printf("  Author: %s\n\n", m.Author)

		if len(m.Parents) > 0 {
			curr = m.Parents[0] // Simple line history traversal
		} else {
			curr = ""
		}
	}
}

func handleVls() {
	handleTree()
}

func handleTree() {
	versions := getAllVersions(LocalRefsDir)
	if len(versions) == 0 {
		fmt.Println("No versions found.")
		return
	}

	branches, _ := os.ReadDir(LocalBranchesDir)
	branchMap := make(map[string][]string)
	for _, b := range branches {
		cid := getBranch(b.Name())
		branchMap[cid] = append(branchMap[cid], b.Name())
	}

	head := getHead()
	headCID := getBranch(head)
	if headCID == "" {
		headCID = head
	}

	fmt.Println("\n🌳 Falcon Version Tree")
	fmt.Println("---------------------------------------------------------------")

	// Sort versions by time descending for display
	sort.Slice(versions, func(i, j int) bool {
		return versions[i].CreatedAt.After(versions[j].CreatedAt)
	})

	for i, v := range versions {
		// node symbol
		symbol := "○"
		if v.CommitID == headCID {
			symbol = "\033[1;32m●\033[0m" // Green dot for HEAD
		} else {
			symbol = "\033[1;33m○\033[0m" // Yellow circle
		}

		// Branch labels
		labels := ""
		if names, ok := branchMap[v.CommitID]; ok {
			for _, name := range names {
				color := "36" // Cyan
				if name == head {
					color = "32;1" // Bold Green
				}
				labels += fmt.Sprintf("\033[%sm[%s]\033[0m ", color, name)
			}
		}

		// Time & Author
		meta := fmt.Sprintf("\033[90m(%s by %s)\033[0m", v.CreatedAt.Format("01-02 15:04"), v.Author)

		// Main commit line
		fmt.Printf("%s  \033[33m%s\033[0m %s%s %s\n", symbol, v.CommitID[:7], labels, v.Description, meta)

		// Connection lines (pseudo-graph)
		if len(v.Parents) > 1 {
			fmt.Printf("│  \033[35m[Merge: %s & %s]\033[0m\n", v.Parents[0][:7], v.Parents[1][:7])
		}

		if i < len(versions)-1 {
			fmt.Println("│")
		}
	}
	fmt.Println()
}

func handleDiff(ver1, ver2 string) {
	m1, err1 := loadManifest(ver1)
	m2, err2 := loadManifest(ver2)
	if err1 != nil || err2 != nil {
		fmt.Printf("[Error] Ensure both versions exist: %v, %v\n", err1, err2)
		return
	}

	map1 := toFileMap(m1.Files)
	map2 := toFileMap(m2.Files)

	fmt.Printf("Diffing: %s -> %s\n", ver1, ver2)
	fmt.Println("---------------------------------------------------------------")

	// Collect all paths
	paths := make(map[string]bool)
	for p := range map1 {
		paths[p] = true
	}
	for p := range map2 {
		paths[p] = true
	}

	// Sort paths for consistent output
	var sortedPaths []string
	for p := range paths {
		sortedPaths = append(sortedPaths, p)
	}
	sort.Strings(sortedPaths)

	for _, path := range sortedPaths {
		f1, in1 := map1[path]
		f2, in2 := map2[path]

		if in1 && !in2 {
			fmt.Printf("\033[31m- [DELETED]  %s\033[0m\n", path)
		} else if !in1 && in2 {
			fmt.Printf("\033[32m+ [ADDED]    %s\033[0m\n", path)
			printDetailedDiff(nil, &f2)
		} else if f1.Hash != f2.Hash {
			fmt.Printf("\033[33m~ [MODIFIED] %s\033[0m\n", path)
			printDetailedDiff(&f1, &f2)
		}
	}
}

func printDetailedDiff(f1, f2 *FileMeta) {
	var d1, d2 []byte
	var err error

	if f1 != nil {
		d1, err = readBlob(f1.Hash)
		if err != nil {
			return
		}
	}
	if f2 != nil {
		d2, err = readBlob(f2.Hash)
		if err != nil {
			return
		}
	}

	if (f1 != nil && isBinary(d1)) || (f2 != nil && isBinary(d2)) {
		fmt.Println("    (Binary files differ)")
		return
	}

	s1 := strings.Split(string(d1), "\n")
	s2 := strings.Split(string(d2), "\n")

	// Basic line-by-line diff (Naive implementation)
	// For better results, a full LCS algorithm should be used, but this shows line changes.
	m := len(s1)
	if len(s2) > m {
		m = len(s2)
	}

	// Simple comparison for demonstration
	// In a real Git-like tool, we'd use LCS.
	diffFound := false
	for i := 0; i < m; i++ {
		l1 := ""
		if i < len(s1) {
			l1 = s1[i]
		}
		l2 := ""
		if i < len(s2) {
			l2 = s2[i]
		}

		if l1 != l2 {
			if !diffFound {
				fmt.Println("    @@ Line Changes @@")
				diffFound = true
			}
			if i < len(s1) {
				fmt.Printf("\033[31m    - %s\033[0m\n", l1)
			}
			if i < len(s2) {
				fmt.Printf("\033[32m    + %s\033[0m\n", l2)
			}
		}
	}
}

func handleGC() {
	defer acquireLock()()
	fmt.Println("[Falcon GC] Scanning for orphaned blobs globally...")

	// 1. Collect all valid hashes across ALL projects
	validHashes := make(map[string]bool)
	err := filepath.Walk(GlobalProjectsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".json") {
			return nil
		}
		var m VersionManifest
		data, err := os.ReadFile(path)
		if err == nil && json.Unmarshal(data, &m) == nil {
			for _, f := range m.Files {
				validHashes[f.Hash] = true
			}
		}
		return nil
	})

	if err != nil {
		fmt.Printf("Failed to scan projects: %v\n", err)
		return
	}

	// 2. Scan blobs and remove unused
	entries, _ := os.ReadDir(GlobalBlobsDir)
	removedCount := 0
	var freedSpace int64

	for _, e := range entries {
		if !e.IsDir() && !validHashes[e.Name()] {
			blobPath := filepath.Join(GlobalBlobsDir, e.Name())
			if info, err := e.Info(); err == nil {
				freedSpace += info.Size()
			}
			os.Remove(blobPath)
			removedCount++
		}
	}

	fmt.Printf("[GC Complete] Removed %d orphaned blobs. Freed %.2f MB.\n", removedCount, float64(freedSpace)/(1024*1024))
}

func getNextVersion(isMajor, isMinor bool) string {
	versions := getAllVersions(LocalRefsDir)
	var latest string

	// Find the most recent actual version (ignore backups)
	for i := len(versions) - 1; i >= 0; i-- {
		if !strings.HasPrefix(versions[i].Version, "backup-") {
			latest = versions[i].Version
			break
		}
	}

	if latest == "" {
		if isMajor {
			return "1.0.0"
		}
		if isMinor {
			return "0.1.0"
		}
		return "0.0.0"
	}

	parts := strings.Split(latest, ".")
	if len(parts) != 3 {
		return latest + "-1"
	}

	major, _ := strconv.Atoi(parts[0])
	minor, _ := strconv.Atoi(parts[1])
	patch, _ := strconv.Atoi(parts[2])

	if isMajor {
		major++
		minor = 0
		patch = 0
	} else if isMinor {
		minor++
		patch = 0
	} else {
		patch++
	}

	return fmt.Sprintf("%d.%d.%d", major, minor, patch)
}

func getAllVersions(refsDir string) []VersionManifest {
	entries, _ := os.ReadDir(refsDir)
	var versions []VersionManifest

	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			m, err := loadManifestFromFile(filepath.Join(refsDir, e.Name()))
			if err == nil {
				versions = append(versions, *m)
			}
		}
	}

	// Semantic Time Sort
	sort.Slice(versions, func(i, j int) bool {
		return versions[i].CreatedAt.Before(versions[j].CreatedAt)
	})
	return versions
}

func scanAndStoreFiles() []FileMeta {
	ignorePatterns, _ := loadIgnorePatterns()
	var files []FileMeta
	index := loadIndex()

	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if shouldIgnore(path, info, ignorePatterns) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}

		var hash string
		cached, ok := index[path]
		if ok && cached.Size == info.Size() && cached.ModTime.Equal(info.ModTime()) {
			hash = cached.Hash
		} else {
			calculatedHash, err := calculateFileHash(path)
			if err != nil {
				fmt.Printf("Hash Error %s: %v\n", path, err)
				return nil
			}
			hash = calculatedHash
			index[path] = IndexEntry{Hash: hash, Size: info.Size(), ModTime: info.ModTime()}

			blobPath := filepath.Join(GlobalBlobsDir, hash)
			if _, err := os.Stat(blobPath); os.IsNotExist(err) {
				if err := copyFileAtomic(path, blobPath); err != nil {
					fmt.Printf("Blob Copy Error: %v\n", err)
					return nil
				}
			}
		}

		files = append(files, FileMeta{
			Path: path,
			Hash: hash,
			Size: info.Size(),
		})
		return nil
	})

	if err != nil {
		fmt.Printf("Walk Error: %v\n", err)
		return nil
	}

	saveIndex(index)
	return files
}

func restoreFiles(manifest *VersionManifest, clean bool) {
	targetFiles := make(map[string]bool)
	index := loadIndex()

	for _, meta := range manifest.Files {
		cleanPath := filepath.Clean(meta.Path)
		if strings.HasPrefix(cleanPath, "..") || filepath.IsAbs(cleanPath) {
			fmt.Printf("[Security Warning] Skipping malicious path: %s\n", meta.Path)
			continue
		}

		targetFiles[cleanPath] = true
		blobPath := filepath.Join(GlobalBlobsDir, meta.Hash)

		if stat, err := os.Stat(cleanPath); err == nil {
			if cached, ok := index[cleanPath]; ok && cached.Hash == meta.Hash && stat.Size() == meta.Size {
				continue
			}
			if currentHash, err := calculateFileHash(cleanPath); err == nil && currentHash == meta.Hash {
				index[cleanPath] = IndexEntry{Hash: meta.Hash, Size: stat.Size(), ModTime: stat.ModTime()}
				continue
			}
		}

		fmt.Printf("  Restoring: %s\n", cleanPath)
		os.MkdirAll(filepath.Dir(cleanPath), 0755)

		if err := copyFileAtomic(blobPath, cleanPath); err != nil {
			fmt.Printf("  [Error] Failed to restore %s\n", cleanPath)
		} else {
			if stat, err := os.Stat(cleanPath); err == nil {
				index[cleanPath] = IndexEntry{Hash: meta.Hash, Size: stat.Size(), ModTime: stat.ModTime()}
			}
		}
	}

	saveIndex(index)

	if clean {
		ignorePatterns, _ := loadIgnorePatterns()
		filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			cleanPath := filepath.Clean(path)
			if shouldIgnore(cleanPath, info, ignorePatterns) {
				return nil
			}
			if !targetFiles[cleanPath] {
				fmt.Printf("  [Cleaning] Removing untracked file: %s\n", cleanPath)
				os.Remove(cleanPath)
				delete(index, cleanPath)
			}
			return nil
		})
		saveIndex(index)
	}
	fmt.Println("Done.")
}
func handleSyncSet(mode, branch, remoteURL string) {
	config := loadConfig()
	config.Sync = SyncStatus{
		Mode:         mode,
		TargetBranch: branch,
		RemoteURL:    remoteURL,
	}
	saveConfig(config)

	// Registry on server
	deviceID, _ := os.Hostname() // Simple Device ID for now (demo)
	resp, err := http.Get(fmt.Sprintf("%s/sync/set?project=%s&branch=%s&mode=%s&id=%s", remoteURL, config.Name, branch, mode, deviceID))
	if err != nil || resp.StatusCode != 200 {
		fmt.Printf("[Error] Failed to connect to server: %v\n", err)
		return
	}
	fmt.Printf("[Falcon Sync] %s set for branch %s on %s\n", mode, branch, remoteURL)

	if mode == SyncMaster {
		startMasterWatcher(remoteURL, config.Name, branch)
	} else if mode == SyncSlave {
		startSlaveListener(remoteURL, config.Name, branch)
	}
}

func handleSyncUnset() {
	config := loadConfig()
	if config.Sync.Mode == SyncNone {
		fmt.Println("No active sync session.")
		return
	}

	deviceID, _ := os.Hostname()
	http.Get(fmt.Sprintf("%s/sync/unset?project=%s&branch=%s&id=%s", config.Sync.RemoteURL, config.Name, config.Sync.TargetBranch, deviceID))

	config.Sync.Mode = SyncNone
	saveConfig(config)
	fmt.Println("[Falcon Sync] Session closed.")
}

func startMasterWatcher(url, project, branch string) {
	fmt.Println("[Master] Watching for changes (3s interval)...")
	t := time.NewTicker(3 * time.Second)
	lastHash := getIndexAggregateHash()

	for range t.C {
		currentHash := getIndexAggregateHash()
		if currentHash != lastHash && lastHash != "" {
			fmt.Println("[Master] Change detected! Auto-syncing...")
			handleCommit(false, false, "Auto-sync (Master)")

			cid := getBranch(getHead())
			m, _ := loadManifest(cid)

			// Push Blobs first
			for _, f := range m.Files {
				blobData, _ := readBlob(f.Hash)
				http.Post(fmt.Sprintf("%s/push/blob?hash=%s", url, f.Hash), "application/octet-stream", strings.NewReader(string(blobData)))
			}

			// Push Manifest
			data, _ := json.Marshal(m)
			http.Post(fmt.Sprintf("%s/push/manifest?project=%s", url, project), "application/json", strings.NewReader(string(data)))
			fmt.Printf("[Master] Successfully synced commit %s\n", cid[:7])
		}
		lastHash = currentHash
	}
}

func getIndexAggregateHash() string {
	index := loadIndex()
	h := ""
	for _, e := range index {
		h += e.Hash
	}
	return h
}

func startSlaveListener(url, project, branch string) {
	fmt.Println("[Slave] Monitoring server for Master updates...")
	t := time.NewTicker(3 * time.Second)
	lastProcessedCommit := getBranch(getHead())

	for range t.C {
		resp, err := http.Get(fmt.Sprintf("%s/pull/manifest?project=%s&id=%s", url, project, branch))
		if err == nil && resp.StatusCode == 200 {
			var m VersionManifest
			json.NewDecoder(resp.Body).Decode(&m)

			if m.CommitID != "" && m.CommitID != lastProcessedCommit {
				fmt.Printf("[Slave] New update from Master: %s\n", m.CommitID[:7])

				for _, f := range m.Files {
					blobPath := filepath.Join(GlobalBlobsDir, f.Hash)
					if _, err := os.Stat(blobPath); os.IsNotExist(err) {
						bResp, _ := http.Get(fmt.Sprintf("%s/pull/blob?hash=%s", url, f.Hash))
						if bResp != nil && bResp.StatusCode == 200 {
							dst, _ := os.Create(blobPath)
							io.Copy(dst, bResp.Body)
							dst.Close()
						}
					}
				}

				handleCheckout(m.CommitID)
				fmt.Println("[Slave] Working directory updated.")
				lastProcessedCommit = m.CommitID
			}
		}
	}
}

func handleGet(id string) {
	defer acquireLock()()
	manifest, err := loadManifest(id)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("[Falcon] Getting manifest '%s'...\n", id)
	restoreFiles(manifest, false)
}

func handleReload(id string, cleanUntracked bool) {
	defer acquireLock()()
	targetManifest, err := loadManifest(id)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	backupTag := fmt.Sprintf("backup-%d", time.Now().Unix())
	fmt.Printf("[Falcon] Creating rollback snapshot (%s)...\n", backupTag)

	currentFiles := scanAndStoreFiles()
	backupManifest := VersionManifest{
		Version:     backupTag,
		CreatedAt:   time.Now(),
		Description: "Auto-backup before reload to " + id,
		Files:       currentFiles,
	}
	atomicWriteJSON(filepath.Join(LocalRefsDir, backupTag+".json"), backupManifest)

	fmt.Printf("[Falcon] Reloading to '%s' (Clean untracked: %v)...\n", id, cleanUntracked)
	restoreFiles(targetManifest, cleanUntracked)
}

func handleRollback() {
	defer acquireLock()()
	versions := getAllVersions(LocalRefsDir)
	var latestBackup string
	for i := len(versions) - 1; i >= 0; i-- {
		if strings.HasPrefix(versions[i].Version, "backup-") {
			latestBackup = versions[i].Version
			break
		}
	}

	if latestBackup == "" {
		fmt.Println("[Error] No rollback backups found.")
		return
	}

	fmt.Printf("[Falcon] Rolling back to state %s...\n", latestBackup)
	manifest, _ := loadManifest(latestBackup)
	restoreFiles(manifest, false)
}
