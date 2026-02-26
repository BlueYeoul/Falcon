package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func handleSLS() {
	config := loadConfig()
	url := config.RemoteURL
	if url == "" {
		url = "http://localhost:50005"
	}
	url = strings.TrimSuffix(url, "/")

	fmt.Printf("[Falcon] Fetching your projects from %s...\n", url)

	client := &http.Client{}
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/list?user=%s", url, config.RemoteUser), nil)

	// 🔑 Add Certificate Headers
	pub, priv, err := EnsureKeys()
	if err == nil {
		deviceID, _ := os.Hostname()
		ts := fmt.Sprintf("%d", time.Now().Unix())
		sig := SignMessage(priv, deviceID+ts)

		req.Header.Set("X-Falcon-Device-ID", deviceID)
		req.Header.Set("X-Falcon-Timestamp", ts)
		req.Header.Set("X-Falcon-Signature", sig)
		fmt.Printf("  Authorized as: %s %x...\n", deviceID, pub[:4])
	}

	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("[Error] Network error fetching list: %v\n", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		fmt.Printf("[Error] Failed to fetch list. Status: %d\n", resp.StatusCode)
		return
	}

	var projects []string
	json.NewDecoder(resp.Body).Decode(&projects)

	fmt.Println("\n☁️  Remote Projects:")
	if len(projects) == 0 {
		fmt.Println("  (No projects found on server)")
	}
	for _, p := range projects {
		fmt.Printf("  - %s\n", p)
	}
	fmt.Println()
}

func handlePls() {
	entries, err := os.ReadDir(GlobalProjectsDir)
	if err != nil {
		fmt.Printf("Error reading global registry: %v\n", err)
		return
	}

	fmt.Println("📦 Falcon Projects (in Global Cache):")
	count := 0
	for _, e := range entries {
		if e.IsDir() {
			fmt.Printf("  - %s\n", e.Name())
			count++
		}
	}
	if count == 0 {
		fmt.Println("  (No projects found.)")
	}
}

func handleLoad(projectName string) {
	srcRefDir := filepath.Join(GlobalProjectsDir, projectName, "refs")
	if _, err := os.Stat(srcRefDir); os.IsNotExist(err) {
		fmt.Printf("Error: Project '%s' not found in global cache.\n", projectName)
		return
	}

	fmt.Printf("[Falcon] Loading project '%s'...\n", projectName)
	os.MkdirAll(projectName, 0755)
	os.Chdir(projectName)
	os.MkdirAll(LocalRefsDir, 0755)
	config := RepoConfig{Name: projectName}
	atomicWriteJSON(LocalConfig, config)

	copyDir(srcRefDir, LocalRefsDir)

	versions := getAllVersions(LocalRefsDir)
	if len(versions) > 0 {
		latestVer := versions[len(versions)-1].Version
		fmt.Printf("Auto-checkout latest version: %s\n", latestVer)
		handleGet(latestVer)
	}
}

func handleFork(sourceName, newName string) {
	if newName == "" {
		newName = sourceName + "_fork"
	}
	srcRefDir := filepath.Join(GlobalProjectsDir, sourceName, "refs")
	if _, err := os.Stat(srcRefDir); os.IsNotExist(err) {
		fmt.Printf("Error: Source project '%s' not found.\n", sourceName)
		return
	}
	fmt.Printf("[Falcon] Forking '%s' -> '%s'...\n", sourceName, newName)
	os.MkdirAll(newName, 0755)
	os.Chdir(newName)
	os.MkdirAll(LocalRefsDir, 0755)

	config := RepoConfig{Name: newName}
	atomicWriteJSON(LocalConfig, config)
	copyDir(srcRefDir, LocalRefsDir)

	versions := getAllVersions(LocalRefsDir)
	if len(versions) > 0 {
		latestVer := versions[len(versions)-1].Version
		manifest, _ := loadManifest(latestVer)
		restoreFiles(manifest, false)
	}
	fmt.Printf("\n✨ Fork Complete! You are now in '%s'.\n", newName)
}

func handlePush() {
	defer acquireLock()()
	config := loadConfig()

	if config.RemoteUser == "" {
		fmt.Println("[Error] Remote not configured. Use 'falcon login <url>' first.")
		return
	}

	url := config.RemoteURL
	if url == "" {
		url = "http://localhost:50005"
	}
	url = strings.TrimSuffix(url, "/")

	pub, _, _ := EnsureKeys()
	deviceID, _ := os.Hostname()
	fmt.Printf("[Falcon] Pushing incremental updates to %s...\n", url)
	if len(pub) > 0 {
		fmt.Printf("  Using Device ID: %s %x...\n", deviceID, pub[:4])
	}

	// 1. Collect all manifests and blobs
	refs, err := os.ReadDir(LocalRefsDir)
	if err != nil {
		fmt.Printf("[Error] Failed to read local refs: %v\n", err)
		return
	}

	blobHashes := make(map[string]bool)
	var manifests []VersionManifest

	for _, r := range refs {
		if !strings.HasSuffix(r.Name(), ".json") {
			continue
		}
		refPath := filepath.Join(LocalRefsDir, r.Name())
		data, _ := os.ReadFile(refPath)
		var m VersionManifest
		if err := json.Unmarshal(data, &m); err == nil {
			manifests = append(manifests, m)
			for _, fileMeta := range m.Files {
				blobHashes[fileMeta.Hash] = true
			}
		}
	}

	// 2. Push Blobs (Granular)
	totalBlobs := len(blobHashes)
	currentBlob := 0
	for hash := range blobHashes {
		currentBlob++
		fmt.Printf("\r  ⬆️  Pushing Blobs: %d/%d (%s...)", currentBlob, totalBlobs, hash[:8])

		path := filepath.Join(GlobalBlobsDir, hash)
		if _, err := os.Stat(path); err != nil {
			fmt.Printf("\n[Error] Blob %s is missing from local cache.\n", hash)
			fmt.Println("  (Hint: Run 'falcon add .' to re-index and regenerate missing blobs)")
			return
		}

		if err := pushBlob(url, hash); err != nil {
			fmt.Printf("\n[Error] Failed to push blob %s: %v\n", hash, err)
			return
		}
	}
	fmt.Println("\n  ✅ All blobs synchronized.")

	// 3. Push Manifests
	for _, m := range manifests {
		fmt.Printf("  ⬆️  Pushing Manifest: %s (%s)\n", m.CommitID, m.Description)
		if err := pushManifest(url, config.RemoteUser, config.Name, m); err != nil {
			fmt.Printf("[Error] Failed to push manifest %s: %v\n", m.CommitID, err)
			return
		}
	}

	fmt.Println("[Success] Repository pushed successfully!")
}

func pushBlob(baseURL, hash string) error {
	path := filepath.Join(GlobalBlobsDir, hash)
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	url := fmt.Sprintf("%s/push/blob?hash=%s", baseURL, hash)
	req, _ := http.NewRequest("POST", url, f)
	req.Header.Set("Content-Type", "application/octet-stream")
	addAuthHeaders(req)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func pushManifest(baseURL, user, project string, m VersionManifest) error {
	data, _ := json.Marshal(m)
	url := fmt.Sprintf("%s/push/manifest?user=%s&project=%s", baseURL, user, project)
	req, _ := http.NewRequest("POST", url, strings.NewReader(string(data)))
	req.Header.Set("Content-Type", "application/json")
	addAuthHeaders(req)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func addAuthHeaders(req *http.Request) {
	_, priv, err := EnsureKeys()
	if err != nil {
		return
	}
	deviceID, _ := os.Hostname()
	ts := fmt.Sprintf("%d", time.Now().Unix())
	sig := SignMessage(priv, deviceID+ts)

	req.Header.Set("X-Falcon-Device-ID", deviceID)
	req.Header.Set("X-Falcon-Timestamp", ts)
	req.Header.Set("X-Falcon-Signature", sig)
}

func handlePull(target string) {
	config := loadConfig()

	var baseURL, user, project string

	if strings.HasPrefix(target, "http") {
		// Detect user/project from URL if possible, or target URL is baseURL
		baseURL = target
		// Simple parsing: http://domain/user/project
		parts := strings.Split(strings.TrimPrefix(strings.TrimPrefix(target, "http://"), "https://"), "/")
		if len(parts) >= 2 {
			user, project = parts[len(parts)-2], parts[len(parts)-1]
		}
	} else {
		parts := strings.Split(target, "/")
		if len(parts) != 2 {
			fmt.Println("Usage: falcon pull <username>/<repo_name> OR <http_url>")
			return
		}
		user, project = parts[0], parts[1]
		baseURL = config.RemoteURL
		if baseURL == "" {
			baseURL = "http://fco.blue-yeoul.com"
		}
	}

	baseURL = strings.TrimSuffix(baseURL, "/")
	// If we are already in a repo, update it. If not, clone it.
	if _, err := os.Stat(LocalRepoDir); err == nil {
		fmt.Printf("[Falcon] Pulling updates for %s/%s...\n", user, project)
		syncRepoGranular(baseURL, user, project)
	} else {
		handleCloneRemoteGranular(baseURL, user, project)
	}
}

func handleCloneRemoteGranular(baseURL, user, project string) {
	fmt.Printf("[Falcon] Cloning %s/%s from %s...\n", user, project, baseURL)

	os.MkdirAll(project, 0755)
	os.Chdir(project)
	initLocalStorage()

	// Update local config
	config := loadConfig()
	config.RemoteURL = baseURL
	config.RemoteUser = user
	config.Name = project
	saveConfig(config)

	syncRepoGranular(baseURL, user, project)
	fmt.Println("[Success] Repository cloned successfully.")
}

func syncRepoGranular(baseURL, user, project string) {
	// 1. Get HEAD
	url := fmt.Sprintf("%s/pull/head?user=%s&project=%s", baseURL, user, project)
	req, _ := http.NewRequest("GET", url, nil)
	addAuthHeaders(req)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != 200 {
		fmt.Printf("[Error] Failed to fetch remote head. Status: %d\n", resp.StatusCode)
		return
	}
	var res map[string]string
	json.NewDecoder(resp.Body).Decode(&res)
	resp.Body.Close()

	headID := res["commit_id"]
	fmt.Printf("  📍 Remote HEAD: %s\n", headID)

	// 2. Fetch Manifests recursively
	fetchQueue := []string{headID}
	fetchedCount := 0

	for len(fetchQueue) > 0 {
		id := fetchQueue[0]
		fetchQueue = fetchQueue[1:]

		manifestPath := filepath.Join(LocalRefsDir, id+".json")
		if _, err := os.Stat(manifestPath); err == nil {
			continue // Already have it
		}

		fmt.Printf("\r  📥 Fetching Manifests: %d...", fetchedCount+1)
		m, err := fetchManifest(baseURL, user, project, id)
		if err != nil {
			fmt.Printf("\n[Error] Failed to fetch manifest %s: %v\n", id, err)
			return
		}
		fetchedCount++

		// Add parents to queue
		fetchQueue = append(fetchQueue, m.Parents...)
	}
	fmt.Println("\n  ✅ All manifests synchronized.")

	// 3. Sync Blobs for the HEAD version
	headManifest, _ := loadManifest(headID)
	totalBlobs := len(headManifest.Files)
	for i, f := range headManifest.Files {
		fmt.Printf("\r  📥 Fetching Blobs: %d/%d (%s...)", i+1, totalBlobs, f.Hash[:8])
		dst := filepath.Join(GlobalBlobsDir, f.Hash)
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		if err := fetchBlob(baseURL, f.Hash); err != nil {
			fmt.Printf("\n[Error] Failed to fetch blob %s: %v\n", f.Hash, err)
		}
	}
	fmt.Println("\n  ✅ Working set blobs synchronized.")

	// 4. Auto-checkout
	handleCheckout(headID)
}

func fetchManifest(baseURL, user, project, id string) (*VersionManifest, error) {
	url := fmt.Sprintf("%s/pull/manifest?user=%s&project=%s&id=%s", baseURL, user, project, id)
	req, _ := http.NewRequest("GET", url, nil)
	addAuthHeaders(req)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != 200 {
		return nil, fmt.Errorf("failed to fetch")
	}
	defer resp.Body.Close()

	var m VersionManifest
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, err
	}

	// Save locally
	path := filepath.Join(LocalRefsDir, id+".json")
	os.MkdirAll(filepath.Dir(path), 0755)
	atomicWriteJSON(path, m)
	return &m, nil
}

func fetchBlob(baseURL, hash string) error {
	url := fmt.Sprintf("%s/pull/blob?hash=%s", baseURL, hash)
	req, _ := http.NewRequest("GET", url, nil)
	addAuthHeaders(req)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != 200 {
		return fmt.Errorf("failed to fetch")
	}
	defer resp.Body.Close()

	dst := filepath.Join(GlobalBlobsDir, hash)
	os.MkdirAll(filepath.Dir(dst), 0755)
	f, _ := os.Create(dst)
	defer f.Close()
	io.Copy(f, resp.Body)
	return nil
}

func packFCO(fcoPath string) error {
	f, err := os.Create(fcoPath)
	if err != nil {
		return err
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	defer zw.Close()

	refs, err := os.ReadDir(LocalRefsDir)
	if err != nil {
		return err
	}

	blobHashes := make(map[string]bool)

	// 1. Pack manifests and extract required blobs
	for _, r := range refs {
		if !strings.HasSuffix(r.Name(), ".json") {
			continue
		}
		refPath := filepath.Join(LocalRefsDir, r.Name())
		b, _ := os.ReadFile(refPath)

		fWriter, _ := zw.Create("refs/" + r.Name())
		fWriter.Write(b)

		var m VersionManifest
		if err := json.Unmarshal(b, &m); err == nil {
			for _, fileMeta := range m.Files {
				blobHashes[fileMeta.Hash] = true
			}
		}
	}

	// 2. Pack required blobs
	for hash := range blobHashes {
		bPath := filepath.Join(GlobalBlobsDir, hash)
		bData, err := os.ReadFile(bPath)
		if err == nil {
			fWriter, _ := zw.Create("blobs/" + hash)
			fWriter.Write(bData)
		}
	}

	return nil
}

func unpackFCO(fcoPath string) error {
	zr, err := zip.OpenReader(fcoPath)
	if err != nil {
		return err
	}
	defer zr.Close()

	os.MkdirAll(LocalRefsDir, 0755)
	os.MkdirAll(GlobalBlobsDir, 0755)

	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			continue
		}

		if strings.HasPrefix(f.Name, "refs/") {
			dst := filepath.Join(LocalRefsDir, filepath.Base(f.Name))
			dstFile, _ := os.Create(dst)
			io.Copy(dstFile, rc)
			dstFile.Close()
		} else if strings.HasPrefix(f.Name, "blobs/") {
			hash := filepath.Base(f.Name)
			dst := filepath.Join(GlobalBlobsDir, hash)
			if _, err := os.Stat(dst); os.IsNotExist(err) {
				dstFile, _ := os.Create(dst)
				io.Copy(dstFile, rc)
				dstFile.Close()
			}
		}
		rc.Close()
	}
	return nil
}
