package main

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// handleLogin provides an integrated onboarding flow.
func handleLogin(serverURL string) {
	if serverURL == "" {
		fmt.Println("Usage: falcon login <server_url>")
		return
	}
	if !strings.HasPrefix(serverURL, "http://") && !strings.HasPrefix(serverURL, "https://") {
		serverURL = "http://" + serverURL
	}
	serverURL = strings.TrimSuffix(serverURL, "/")

	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Are you an existing user? [y/N]: ")
	ans, _ := reader.ReadString('\n')
	ans = strings.TrimSpace(strings.ToLower(ans))

	if ans == "y" {
		fmt.Print("Do you have a trust code from another device? [y/N]: ")
		trustAns, _ := reader.ReadString('\n')
		if strings.TrimSpace(strings.ToLower(trustAns)) == "y" {
			fmt.Print("Enter 8-digit trust code: ")
			code, _ := reader.ReadString('\n')
			handleTrustUse(serverURL, strings.TrimSpace(code))
			return
		}
		// Otherwise, just setup with existing username
		fmt.Print("Enter your username: ")
		username, _ := reader.ReadString('\n')
		username = strings.TrimSpace(username)

		config := loadConfig()
		config.RemoteUser = username
		config.RemoteURL = serverURL
		saveConfig(config)

		fmt.Printf("Logged in as %s. If this is a new device, remember to pair it using 'falcon trust' on your main machine.\n", username)
	} else {
		fmt.Print("Choose a new username: ")
		username, _ := reader.ReadString('\n')
		username = strings.TrimSpace(username)
		if username == "" {
			return
		}

		// 1. Generate and Register Key
		pub, _, _ := EnsureKeys()
		deviceID, _ := os.Hostname()
		fmt.Printf("Registering device '%s' with server...\n", deviceID)

		resp, err := http.Post(fmt.Sprintf("%s/auth/register?id=%s&user=%s", serverURL, deviceID, username), "application/octet-stream", bytes.NewReader(pub))
		if err != nil {
			fmt.Printf("[Error] Network error during registration: %v\n", err)
			fmt.Println("Check if the server is running and the URL is correct.")
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			fmt.Printf("[Error] Server registration failed. Status: %d, Response: %s\n", resp.StatusCode, string(body))
			return
		}

		// 2. Save config
		config := loadConfig()
		config.RemoteUser = username
		config.RemoteURL = serverURL
		saveConfig(config)

		fmt.Printf("✨ Welcome to Falcon, %s! Device registered and logged in.\n", username)
	}
}

func handleTrustGen() {
	config := loadConfig()
	url := config.RemoteURL
	if url == "" {
		fmt.Println("[Error] Remote URL not set. Use 'falcon login <url>' first.")
		return
	}
	url = strings.TrimSuffix(url, "/")

	client := &http.Client{}
	req, _ := http.NewRequest("GET", url+"/auth/trust/gen", nil)

	// Sign with current device key
	_, priv, err := EnsureKeys()
	if err == nil {
		deviceID, _ := os.Hostname()
		ts := fmt.Sprintf("%d", time.Now().Unix())
		sig := SignMessage(priv, deviceID+ts)
		req.Header.Set("X-Falcon-Device-ID", deviceID)
		req.Header.Set("X-Falcon-Timestamp", ts)
		req.Header.Set("X-Falcon-Signature", sig)
	}

	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("[Error] Network error: %v\n", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		fmt.Printf("[Error] Failed to generate trust code. Status: %v\n", resp.StatusCode)
		return
	}

	var res map[string]string
	json.NewDecoder(resp.Body).Decode(&res)

	fmt.Println("\n🛡️  Falcon Device Trust")
	fmt.Printf("Your 8-digit Pairing Code: \033[1;32m%s\033[0m\n", res["code"])
	fmt.Println("This code is valid for 5 minutes.")
	fmt.Println("On your NEW computer, run: falcon login <server_url> (choose Existing User -> Trust Code)")
}

func handleTrustUse(serverURL, code string) {
	pub, _, err := EnsureKeys()
	if err != nil {
		return
	}
	deviceID, _ := os.Hostname()
	serverURL = strings.TrimSuffix(serverURL, "/")

	fmt.Printf("[Falcon] Pairing this device with code %s...\n", code)

	resp, err := http.Post(fmt.Sprintf("%s/auth/trust/use?code=%s&id=%s", serverURL, code, deviceID), "application/octet-stream", strings.NewReader(string(pub)))
	if err != nil {
		fmt.Printf("[Error] Network error during pairing: %v\n", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		fmt.Printf("[Error] Pairing failed. Check your code or server status. (Status: %v)\n", resp.StatusCode)
		return
	}

	var res map[string]string
	json.NewDecoder(resp.Body).Decode(&res)

	config := loadConfig()
	config.RemoteUser = res["username"]
	config.RemoteURL = serverURL
	saveConfig(config)

	fmt.Printf("\n✅ Success! This device is now paired with User: %s\n", res["username"])
}

func EnsureKeys() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	if _, err := os.Stat(LocalPrivateKeyFile); os.IsNotExist(err) {
		os.MkdirAll(LocalKeyDir, 0700)
		pub, priv, _ := ed25519.GenerateKey(rand.Reader)
		os.WriteFile(LocalPrivateKeyFile, priv, 0600)
		return pub, priv, nil
	}
	priv, _ := os.ReadFile(LocalPrivateKeyFile)
	pk := ed25519.PrivateKey(priv)
	return pk.Public().(ed25519.PublicKey), pk, nil
}

func SignMessage(priv ed25519.PrivateKey, message string) string {
	sig := ed25519.Sign(priv, []byte(message))
	return hex.EncodeToString(sig)
}

func VerifySignature(pub ed25519.PublicKey, message, signature string) bool {
	sig, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	return ed25519.Verify(pub, []byte(message), sig)
}

func verifyRequestAuth(r *http.Request) bool {
	deviceID := r.Header.Get("X-Falcon-Device-ID")
	signature := r.Header.Get("X-Falcon-Signature")
	timestamp := r.Header.Get("X-Falcon-Timestamp")

	if deviceID == "" || signature == "" || timestamp == "" {
		return false
	}

	keyPath := filepath.Join(ServerKeysDir, deviceID+".pub")
	pubKeyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		fmt.Printf("[Auth] Key not found for device %s: %v\n", deviceID, err)
		return false
	}

	if len(pubKeyBytes) != ed25519.PublicKeySize {
		fmt.Printf("[Auth] Invalid key size for device %s: %d bytes (expected %d)\n", deviceID, len(pubKeyBytes), ed25519.PublicKeySize)
		return false
	}

	message := deviceID + timestamp
	valid := VerifySignature(pubKeyBytes, message, signature)
	if !valid {
		fmt.Printf("[Auth] Signature verification failed for device %s\n", deviceID)
		return false
	}

	// 🕒 Prevent Replay Attacks: Check if timestamp is within 15 minutes
	tsInt, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		fmt.Printf("[Auth] Invalid timestamp: %s\n", timestamp)
		return false
	}
	diff := time.Now().Unix() - tsInt
	if diff < -300 || diff > 900 { // Allow 5 min clock skew, 15 min expiration
		fmt.Printf("[Auth] Signature expired (diff: %ds)\n", diff)
		return false
	}

	return true
}

func handleRegis(username, token string) {
	config := loadConfig()
	config.RemoteUser = username
	config.RemoteHash = token
	saveConfig(config)
	fmt.Printf("Registered remote user: %s\n", username)
}

func handleUserRename(newName string) {
	config := loadConfig()
	url := config.RemoteURL
	if url == "" {
		fmt.Println("[Error] Remote URL not set. Use 'falcon login <url>' first.")
		return
	}
	url = strings.TrimSuffix(url, "/")

	oldName := config.RemoteUser
	fmt.Printf("[Falcon] Renaming identity: %s -> %s...\n", oldName, newName)

	client := &http.Client{}
	req, _ := http.NewRequest("POST", fmt.Sprintf("%s/auth/rename?newname=%s", url, newName), nil)

	// Sign with current device key
	_, priv, err := EnsureKeys()
	if err == nil {
		deviceID, _ := os.Hostname()
		ts := fmt.Sprintf("%d", time.Now().Unix())
		sig := SignMessage(priv, deviceID+ts)
		req.Header.Set("X-Falcon-Device-ID", deviceID)
		req.Header.Set("X-Falcon-Timestamp", ts)
		req.Header.Set("X-Falcon-Signature", sig)
	}

	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("[Error] Network error during rename: %v\n", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		config.RemoteUser = newName
		config.Author = newName
		saveConfig(config)
		fmt.Printf("✅ Success! Identity updated to: %s\n", newName)
	} else {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("❌ Failed to rename. Status: %d, Response: %s\n", resp.StatusCode, string(body))
	}
}

func handleKeyGen(serverURL string) {
	pub, _, err := EnsureKeys()
	if err != nil {
		return
	}
	deviceID, _ := os.Hostname()
	if serverURL != "" {
		http.Post(fmt.Sprintf("%s/auth/register?id=%s", serverURL, deviceID), "application/octet-stream", bytes.NewReader(pub))
	}
}
