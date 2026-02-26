package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ServerState tracks active sync sessions and their states
type ServerState struct {
	mu           sync.Mutex
	Sessions     map[string]*SyncSession // branch -> session
	PairingCodes map[string]PairingInfo  // code -> info
}

type PairingInfo struct {
	Username  string
	ExpiresAt time.Time
}

var state = &ServerState{
	Sessions:     make(map[string]*SyncSession),
	PairingCodes: make(map[string]PairingInfo),
}

func startServer(port string) {
	os.MkdirAll(ServerBlobsDir, 0755)
	os.MkdirAll(ServerRefsDir, 0755)
	os.MkdirAll(ServerKeysDir, 0755)
	os.MkdirAll(ServerProjectsDir, 0755)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("[Server] %s %s from %s\n", r.Method, r.URL.Path, r.RemoteAddr)
		if strings.HasPrefix(r.URL.Path, "/auth/register") {
			handleRegisterKey(w, r)
			return
		} else if r.URL.Path == "/auth/trust/gen" {
			handleAuthTrustGen(w, r)
		} else if r.URL.Path == "/auth/trust/use" {
			handleAuthTrustUse(w, r)
		} else if r.URL.Path == "/auth/rename" {
			handleAuthRename(w, r)
		} else if strings.HasPrefix(r.URL.Path, "/sync/") {
			if strings.HasSuffix(r.URL.Path, "/set") {
				handleSetSync(w, r)
			} else if strings.HasSuffix(r.URL.Path, "/unset") {
				handleUnsetSync(w, r)
			} else if strings.HasSuffix(r.URL.Path, "/status") {
				handleGetSyncStatus(w, r)
			}
		} else if r.URL.Path == "/list" {
			handleListProjects(w, r)
		} else if strings.HasPrefix(r.URL.Path, "/push/") {
			if strings.HasSuffix(r.URL.Path, "/manifest") {
				handlePushManifest(w, r)
			} else if strings.HasSuffix(r.URL.Path, "/blob") {
				handlePushBlob(w, r)
			}
		} else if strings.HasPrefix(r.URL.Path, "/pull/") {
			if strings.HasSuffix(r.URL.Path, "/manifest") {
				handlePullManifest(w, r)
			} else if strings.HasSuffix(r.URL.Path, "/blob") {
				handlePullBlob(w, r)
			}
		} else {
			handleFCOStorage(w, r)
		}
	})

	fmt.Printf("🦅 Falcon Sync Server starting on :%s\n", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		fmt.Printf("Server Error: %v\n", err)
	}
}

func handleServerReset() {
	if os.Getuid() != 0 {
		fmt.Println("\n❌ [Error] This operation requires administrator privileges (sudo).")
		return
	}

	reader := bufio.NewReader(os.Stdin)
	fmt.Println("\n⚠️  WARNING: You are about to DELETE ALL Falcon server data.")
	fmt.Println("This includes all project repositories, blobs, and registered device keys.")
	fmt.Println("This action CANNOT be undone.")

	for i := 1; i <= 3; i++ {
		fmt.Printf("\n[%d/3] Are you REALLY sure you want to initialize? (y/N): ", i)
		ans, _ := reader.ReadString('\n')
		ans = strings.TrimSpace(strings.ToLower(ans))
		if ans != "y" {
			fmt.Println("🛑 [Abort] Reset operation cancelled.")
			return
		}
	}

	fmt.Printf("\n🗑️  [Server] Deleting everything in %s...\n", ServerBaseDir)
	err := os.RemoveAll(ServerBaseDir)
	if err != nil {
		fmt.Printf("❌ [Error] Reset failed: %v\n", err)
	} else {
		// Re-create basic structure
		os.MkdirAll(ServerBlobsDir, 0755)
		os.MkdirAll(ServerRefsDir, 0755)
		os.MkdirAll(ServerKeysDir, 0755)
		os.MkdirAll(ServerProjectsDir, 0755)
		fmt.Println("✅ [Success] Server storage has been completely initialized.")
	}
}

// Certificate-based Auth Placeholder
// In a real implementation, this would verify a signature.
func handleRegisterKey(w http.ResponseWriter, r *http.Request) {
	deviceID := r.URL.Query().Get("id")
	username := r.URL.Query().Get("user")
	if username == "" {
		username = deviceID
	}

	keyPath := filepath.Join(ServerKeysDir, deviceID+".pub")
	f, err := os.Create(keyPath)
	if err != nil {
		http.Error(w, "Failed to create key file: "+err.Error(), 500)
		return
	}
	defer f.Close()
	io.Copy(f, r.Body)

	// Store owner mapping
	err = os.WriteFile(filepath.Join(ServerKeysDir, deviceID+".owner"), []byte(username), 0644)
	if err != nil {
		http.Error(w, "Failed to save owner info: "+err.Error(), 500)
		return
	}

	fmt.Printf("[Server] Registered device: %s for user: %s\n", deviceID, username)
	fmt.Fprintf(w, "Registered key for %s\n", username)
}

func handleAuthTrustGen(w http.ResponseWriter, r *http.Request) {
	if !verifyRequestAuth(r) {
		http.Error(w, "Unauthorized", 401)
		return
	}

	deviceID := r.Header.Get("X-Falcon-Device-ID")
	// Look up owner
	ownerBytes, _ := os.ReadFile(filepath.Join(ServerKeysDir, deviceID+".owner"))
	username := string(ownerBytes)
	if username == "" {
		username = deviceID
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	// Generate 8-digit OTP
	code := fmt.Sprintf("%08d", time.Now().UnixNano()%100000000)
	state.PairingCodes[code] = PairingInfo{
		Username:  username,
		ExpiresAt: time.Now().Add(5 * time.Minute),
	}

	fmt.Printf("[Server] Pairing code generated for %s: %s (valid for 5m)\n", username, code)
	json.NewEncoder(w).Encode(map[string]string{"code": code})
}

func handleAuthTrustUse(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	newDeviceID := r.URL.Query().Get("id")

	state.mu.Lock()
	defer state.mu.Unlock()

	info, exists := state.PairingCodes[code]
	if !exists || time.Now().After(info.ExpiresAt) {
		delete(state.PairingCodes, code)
		http.Error(w, "Invalid or expired code", 403)
		return
	}

	// Link new device to the same "User" (folder)
	// For now, we'll store the new device's key and an alias mapping
	keyPath := filepath.Join(ServerKeysDir, newDeviceID+".pub")
	f, _ := os.Create(keyPath)
	defer f.Close()
	io.Copy(f, r.Body)

	// Store same owner for the new device
	os.WriteFile(filepath.Join(ServerKeysDir, newDeviceID+".owner"), []byte(info.Username), 0644)

	// In a real system, we'd have a DB mapping multiple DeviceIDs to one UserID.
	// Here, we'll just create a symlink or a reference file so verifyRequestAuth
	// can find the folder.
	// Since we use deviceID as user folder, we'll just tell the user their 'RemoteUser' is the old one.

	delete(state.PairingCodes, code)
	fmt.Printf("[Server] Device %s paired with User %s\n", newDeviceID, info.Username)
	json.NewEncoder(w).Encode(map[string]string{"username": info.Username})
}

func handleAuthRename(w http.ResponseWriter, r *http.Request) {
	if !verifyRequestAuth(r) {
		http.Error(w, "Unauthorized", 401)
		return
	}

	deviceID := r.Header.Get("X-Falcon-Device-ID")
	newName := r.URL.Query().Get("newname")
	if newName == "" {
		http.Error(w, "newname required", 400)
		return
	}

	// 1. Get old username
	ownerFile := filepath.Join(ServerKeysDir, deviceID+".owner")
	oldNameBytes, _ := os.ReadFile(ownerFile)
	oldName := string(oldNameBytes)

	// 2. Update owner mapping for THIS device
	os.WriteFile(ownerFile, []byte(newName), 0644)

	// 3. (Optional) Rename project directory on server
	if oldName != "" && oldName != newName {
		oldPath := filepath.Join(ServerProjectsDir, oldName)
		newPath := filepath.Join(ServerProjectsDir, newName)
		if _, err := os.Stat(oldPath); err == nil {
			os.Rename(oldPath, newPath)
			fmt.Printf("[Server] Migrated storage: %s -> %s\n", oldName, newName)
		}
	}

	fmt.Printf("[Server] Renamed user for device %s: %s -> %s\n", deviceID, oldName, newName)
	fmt.Fprintf(w, "OK")
}

func handleSetSync(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	branch := r.URL.Query().Get("branch")
	mode := r.URL.Query().Get("mode") // master or slave
	deviceID := r.URL.Query().Get("id")

	state.mu.Lock()
	defer state.mu.Unlock()

	key := project + ":" + branch
	session, exists := state.Sessions[key]
	if !exists {
		session = &SyncSession{
			ID:        key,
			Project:   project,
			Branch:    branch,
			CreatedAt: time.Now(),
		}
		state.Sessions[key] = session
	}

	if mode == SyncMaster {
		session.MasterID = deviceID
		fmt.Printf("[Server] Master set for %s/%s: %s\n", project, branch, deviceID)
	} else if mode == SyncSlave {
		// Avoid duplicate slaves
		found := false
		for _, s := range session.Slaves {
			if s == deviceID {
				found = true
				break
			}
		}
		if !found {
			session.Slaves = append(session.Slaves, deviceID)
		}
		fmt.Printf("[Server] Slave added for %s/%s: %s\n", project, branch, deviceID)
	}

	json.NewEncoder(w).Encode(session)
}

func handleUnsetSync(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	branch := r.URL.Query().Get("branch")
	deviceID := r.URL.Query().Get("id")

	state.mu.Lock()
	defer state.mu.Unlock()

	key := project + ":" + branch
	session, exists := state.Sessions[key]
	if !exists {
		return
	}

	if session.MasterID == deviceID {
		delete(state.Sessions, key)
		fmt.Printf("[Server] Session destroyed (Master %s disconnected for %s)\n", deviceID, key)
	} else {
		newSlaves := []string{}
		for _, s := range session.Slaves {
			if s != deviceID {
				newSlaves = append(newSlaves, s)
			}
		}
		session.Slaves = newSlaves
		fmt.Printf("[Server] Slave %s disconnected for %s\n", deviceID, key)
	}
	fmt.Fprintf(w, "Sync unset for %s\n", deviceID)
}

func handleGetSyncStatus(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	branch := r.URL.Query().Get("branch")

	state.mu.Lock()
	defer state.mu.Unlock()

	key := project + ":" + branch
	if s, exists := state.Sessions[key]; exists {
		json.NewEncoder(w).Encode(s)
	} else {
		http.Error(w, "Session not found", 404)
	}
}

// Data synchronization endpoints
func handleListProjects(w http.ResponseWriter, r *http.Request) {
	// 🔒 Security Check
	if !verifyRequestAuth(r) {
		http.Error(w, "Unauthorized", 401)
		return
	}

	deviceID := r.Header.Get("X-Falcon-Device-ID")
	username := r.URL.Query().Get("user")
	if username == "" {
		username = deviceID
	}

	userDir := filepath.Join(ServerProjectsDir, username)
	entries, err := os.ReadDir(userDir)
	if err != nil {
		json.NewEncoder(w).Encode([]string{})
		return
	}

	var projects []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".fco") {
			projects = append(projects, strings.TrimSuffix(e.Name(), ".fco"))
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(projects)
}

func handlePushManifest(w http.ResponseWriter, r *http.Request) {
	projectName := r.URL.Query().Get("project")
	var m VersionManifest
	json.NewDecoder(r.Body).Decode(&m)

	id := m.CommitID
	path := filepath.Join(ServerRefsDir, projectName, id+".json")
	os.MkdirAll(filepath.Dir(path), 0755)
	atomicWriteJSON(path, m)
	fmt.Printf("[Server] Manifest saved: %s for %s\n", id, projectName)
}

func handlePushBlob(w http.ResponseWriter, r *http.Request) {
	hash := r.URL.Query().Get("hash")
	path := filepath.Join(ServerBlobsDir, hash)
	if _, err := os.Stat(path); err == nil {
		return // Already exists (content-addressable backup!)
	}
	f, _ := os.Create(path)
	defer f.Close()
	io.Copy(f, r.Body)
	fmt.Printf("[Server] Blob backed up: %s\n", hash)
}

func handlePullManifest(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	id := r.URL.Query().Get("id")
	path := filepath.Join(ServerRefsDir, project, id+".json")
	http.ServeFile(w, r, path)
}

func handlePullBlob(w http.ResponseWriter, r *http.Request) {
	hash := r.URL.Query().Get("hash")
	path := filepath.Join(ServerBlobsDir, hash)
	http.ServeFile(w, r, path)
}

func handleFCOStorage(w http.ResponseWriter, r *http.Request) {
	// Parse /{user}/{project}.fco
	path := strings.Trim(r.URL.Path, "/")
	if !strings.HasSuffix(path, ".fco") {
		http.NotFound(w, r)
		return
	}

	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	username, projectFile := parts[0], parts[1]

	// 🔒 Security Check
	if !verifyRequestAuth(r) {
		http.Error(w, "Unauthorized: Invalid Device Certificate", 401)
		return
	}

	storageDir := filepath.Join(ServerProjectsDir, username)
	os.MkdirAll(storageDir, 0755)
	storagePath := filepath.Join(storageDir, projectFile)

	if r.Method == "PUT" {
		fmt.Printf("[Server] Storing uploaded .fco: %s for %s\n", projectFile, username)
		f, _ := os.Create(storagePath)
		defer f.Close()
		io.Copy(f, r.Body)
		fmt.Fprintf(w, "[Success] .fco saved on server.\n")
	} else if r.Method == "GET" {
		if _, err := os.Stat(storagePath); os.IsNotExist(err) {
			http.Error(w, "Project not found", 404)
			return
		}
		fmt.Printf("[Server] Serving .fco: %s for %s\n", projectFile, username)
		http.ServeFile(w, r, storagePath)
	} else {
		http.Error(w, "Method not allowed", 405)
	}
}
