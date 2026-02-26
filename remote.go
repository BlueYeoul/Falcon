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

	fmt.Println("[Falcon] Packing repository into .fco archive...")
	tmpFile := fmt.Sprintf("%s.fco", config.Name)
	if err := packFCO(tmpFile); err != nil {
		fmt.Printf("[Error] Failed to pack: %v\n", err)
		return
	}
	defer os.Remove(tmpFile)

	url := config.RemoteURL
	if url == "" {
		url = fmt.Sprintf("http://localhost:50005/%s/%s.fco", config.RemoteUser, config.Name)
	} else if !strings.HasSuffix(url, ".fco") {
		url = fmt.Sprintf("%s/%s/%s.fco", strings.TrimSuffix(url, "/"), config.RemoteUser, config.Name)
	}

	fmt.Printf("[Falcon] Pushing to %s...\n", url)

	f, err := os.Open(tmpFile)
	if err != nil {
		return
	}
	defer f.Close()

	req, _ := http.NewRequest("PUT", url, f)
	req.Header.Set("Content-Type", "application/octet-stream")

	// 🔑 Add Certificate Headers
	pub, priv, err := EnsureKeys()
	if err == nil {
		deviceID, _ := os.Hostname()
		ts := fmt.Sprintf("%d", time.Now().Unix())
		sig := SignMessage(priv, deviceID+ts)

		req.Header.Set("X-Falcon-Device-ID", deviceID)
		req.Header.Set("X-Falcon-Timestamp", ts)
		req.Header.Set("X-Falcon-Signature", sig)
		fmt.Printf("  Using Device ID: %s %x...\n", deviceID, pub[:4])
	}

	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("[Error] Network error: %v\n", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		fmt.Println("[Success] Repository pushed successfully!")
	} else {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("[Error] Server rejected push. Status: %d, Message: %s\n", resp.StatusCode, string(body))
	}
}

func handlePull(target string) {
	if strings.HasPrefix(target, "http") {
		handleCloneRemote(target)
		return
	}

	parts := strings.Split(target, "/")
	if len(parts) != 2 {
		fmt.Println("Usage: falcon pull <username>/<repo_name> OR <http_url>")
		return
	}
	username, repoName := parts[0], parts[1]

	// ... legacy logic ...
	handleCloneRemote(fmt.Sprintf("https://falcon.blue-yeoul.com/%s/falcon/%s.fco", username, repoName))
}

func handleCloneRemote(url string) {
	// Extract project name from URL
	parts := strings.Split(url, "/")
	projectName := "cloned_repo"
	if len(parts) > 0 {
		projectName = strings.TrimSuffix(parts[len(parts)-1], ".fco")
	}

	fmt.Printf("[Falcon] Cloning %s...\n", url)

	// Create request with Auth Headers
	client := &http.Client{}
	req, _ := http.NewRequest("GET", url, nil)

	// 🔑 Add Certificate Headers
	pub, priv, err := EnsureKeys()
	if err == nil {
		deviceID, _ := os.Hostname()
		ts := fmt.Sprintf("%d", time.Now().Unix())
		sig := SignMessage(priv, deviceID+ts)

		req.Header.Set("X-Falcon-Device-ID", deviceID)
		req.Header.Set("X-Falcon-Timestamp", ts)
		req.Header.Set("X-Falcon-Signature", sig)
		fmt.Printf("  Using Device ID: %s %x...\n", deviceID, pub[:4])
	}

	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != 200 {
		code := 0
		if resp != nil {
			code = resp.StatusCode
		}
		fmt.Printf("[Error] Failed to clone. Status: %d\n", code)
		if code == 401 {
			fmt.Println("  (Hint: Register your device key with 'falcon keygen [server_url]' first)")
		}
		return
	}
	defer resp.Body.Close()

	os.MkdirAll(projectName, 0755)
	os.Chdir(projectName)

	tmpFile := projectName + ".fco"
	f, _ := os.Create(tmpFile)
	io.Copy(f, resp.Body)
	f.Close()
	defer os.Remove(tmpFile)

	fmt.Println("[Falcon] Unpacking repository...")
	if err := unpackFCO(tmpFile); err != nil {
		fmt.Printf("[Error] Unpack failed: %v\n", err)
		return
	}

	// Init local head if missing
	initLocalStorage()

	// Try to checkout latest
	versions := getAllVersions(LocalRefsDir)
	if len(versions) > 0 {
		latestVer := versions[len(versions)-1].CommitID
		if latestVer == "" {
			latestVer = versions[len(versions)-1].Version
		}
		handleCheckout(latestVer)
	}
	fmt.Println("[Success] Repository cloned successfully.")
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
