# Falcon

-   **Git-like 3-Stage Architecture**: Full support for `add` (staging), `status`, `commit`, and `push`/`pull`.
-   **Graph-Based VCS (DAG)**: Commits track their parents, allowing for complex historical branching and merging with conflict detection.
-   **Content-Addressable Storage**: Every file is uniquely identified by its SHA-256 hash in a global cache, preventing data duplication across projects.
-   **Deep Learning Optimized**: Specialized in handling large binary assets (weights, datasets) without slowing down the local working tree.
-   **Verifiable History**: Each commit hashes its entire file set and metadata, ensuring a tamper-proof chain of states.

## 🚀 Quick Installation

Run the installation script to build and install Falcon as both `falcon` and `fco`. This project is hosted at [https://github.com/BlueYeoul/Falcon.git](https://github.com/BlueYeoul/Falcon.git).

```bash
chmod +x install.sh
./install.sh
```

### 🆙 Keeping Falcon Up-to-Date
You can update Falcon to the latest version anytime from within the CLI:
```bash
fco update
```

## 🛠 Git-style Usage Guide

### 1. Initialize & Stage
```bash
falcon init
falcon status             # Check local changes
falcon add .              # Stage all files for commit
```

### 2. Commit & History
```bash
# Create a local commit (DAG node)
falcon commit -m "Add new transformer layer"

# View history with commit IDs
falcon log
```

### 3. Branching & Merging
```bash
# Create and switch to a branch
falcon branch experiment-A
falcon checkout experiment-A

# Merge experiment back to main
falcon checkout main
falcon merge experiment-A
```

### 4. Remote Collaboration
```bash
# Push current branch to remote server
falcon regis <user> -t <tok>
falcon push

# Pull shared state
falcon pull <user>/<repo>
```

### 5. Advanced Recovery
```bash
# Restore working tree to a specific commit or branch
falcon checkout <commit_id|branch_name>

# Garbage collect unused blobs to free disk space
falcon gc
```

### 4. Remote Sync
```bash
# Configure remote user and token
falcon regis <username> -t <your_token>

# Upload project
falcon publish

# Download project
falcon clone <username>/<repo_name>
```

### 5. Maintenance
```bash
# List all projects in global cache
falcon pls

# Garbage collect orphaned blobs (freed up disk space)
falcon gc
```

## 📁 Repository Structure

-   `.falcon/`: Local repository metadata.
    -   `refs/`: Version manifests (JSON).
    -   `index.json`: File status cache.
    -   `config.json`: Repository configuration.
-   `~/.cache/falcon_cache/`: Global storage.
    -   `blobs/`: All file content stored by hash.
    -   `projects/`: Synced manifests for across-the-board management.

## 📝 Ignoring Files
Create a `.fignore` file in your root directory to exclude files (e.g., `node_modules`, `.git`, `*.log`).

---
*Built with ❤️ for AI Research Engineers.*
