package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const Version = "v0.0.1+12b"

func main() {
	initGlobalStorage()

	if len(os.Args) < 2 {
		printUsage()
		return
	}

	cmd := os.Args[1]

	// Subcommands and flags
	initCmd := flag.NewFlagSet("init", flag.ExitOnError)
	initName := initCmd.String("n", "", "Create directory and init repo inside it")

	pushCmd := flag.NewFlagSet("push", flag.ExitOnError)
	pushMinor := pushCmd.Bool("M", false, "Increment MINOR version")
	pushMajor := pushCmd.Bool("R", false, "Increment MAJOR version")
	pushDesc := pushCmd.String("m", "Commit via Falcon CLI", "Description of the version")

	regisCmd := flag.NewFlagSet("regis", flag.ExitOnError)
	regisToken := regisCmd.String("t", "", "Access token/hash for remote repo")

	switch cmd {
	case "init":
		initCmd.Parse(os.Args[2:])
		handleInit(*initName)
	case "add":
		if len(os.Args) < 3 {
			fmt.Println("Usage: falcon add <path>")
			return
		}
		handleAdd(os.Args[2:])
	case "reset":
		if len(os.Args) < 3 {
			handleReset([]string{"."}) // Default to all if no path
		} else {
			handleReset(os.Args[2:])
		}
	case "status":
		handleStatus()
	case "commit":
		pushCmd.Parse(os.Args[2:])
		desc := *pushDesc
		if desc == "Pushed via Falcon CLI" && len(pushCmd.Args()) > 0 {
			desc = strings.Join(pushCmd.Args(), " ")
		}
		handleCommit(*pushMajor, *pushMinor, desc)
	case "log":
		handleLog()
	case "push":
		handlePush()
	case "checkout":
		if len(os.Args) < 3 {
			fmt.Println("Usage: falcon checkout <branch|commit_id>")
			return
		}
		handleCheckout(os.Args[2])
	case "branch":
		if len(os.Args) < 2 {
			handleBranchList()
		} else {
			if len(os.Args) == 2 {
				handleBranchList()
			} else {
				handleBranchCreate(os.Args[2])
			}
		}
	case "merge":
		if len(os.Args) < 3 {
			fmt.Println("Usage: falcon merge <branch>")
			return
		}
		handleMerge(os.Args[2])
	case "gc":
		handleGC()
	case "unlock":
		os.Remove(LocalLockFile)
		fmt.Println("[Falcon] Repository unlocked.")
	case "regis":
		regisCmd.Parse(os.Args[2:])
		if regisCmd.NArg() < 1 {
			fmt.Println("Usage: falcon regis <username> -t <token>")
			return
		}
		handleRegis(regisCmd.Arg(0), *regisToken)
	case "serve":
		serveCmd := flag.NewFlagSet("serve", flag.ExitOnError)
		reset := serveCmd.Bool("reset", false, "Reset server storage (requires admin)")
		serveCmd.Parse(os.Args[2:])

		if *reset {
			handleServerReset()
			return
		}

		port := "50005"
		if serveCmd.NArg() > 0 {
			port = serveCmd.Arg(0)
		}
		startServer(port)
	case "set":
		if len(os.Args) < 5 {
			fmt.Println("Usage: falcon set <master|slave> <branch> <remote_url>")
			return
		}
		handleSyncSet(os.Args[2], os.Args[3], os.Args[4])
	case "unset":
		handleSyncUnset()
	case "login":
		server := ""
		if len(os.Args) >= 3 {
			server = os.Args[2]
		}
		handleLogin(server)
	case "keygen":
		server := ""
		if len(os.Args) >= 3 {
			server = os.Args[2]
		}
		handleKeyGen(server)
	case "pull":
		if len(os.Args) < 3 {
			fmt.Println("Usage: falcon pull <url|repo>")
			return
		}
		handlePull(os.Args[2])
	case "sls":
		handleSLS()
	case "rm":
		if len(os.Args) < 3 {
			fmt.Println("Usage: falcon rm <project_name>")
			return
		}
		handleRm(os.Args[2])
	case "trust":
		if len(os.Args) == 2 {
			handleTrustGen()
		} else if len(os.Args) == 4 {
			handleTrustUse(os.Args[2], os.Args[3])
		} else {
			fmt.Println("Usage:")
			fmt.Println("  Generate code: falcon trust")
			fmt.Println("  Use code:      falcon trust <server_url> <code>")
		}
	case "version":
		fmt.Printf("🦅 Falcon CLI Version: %s\n", Version)
	case "user":
		if len(os.Args) < 3 {
			fmt.Println("Usage: falcon user <new_username>")
			return
		}
		handleUserRename(os.Args[2])
	case "forget":
		handleForget()
	case "vls", "tree":
		handleVls()
	case "update":
		handleUpdate()
	default:
		printUsage()
	}
}

func printUsage() {
	usage := `🦅 Falcon - Deep Learning Optimized VCS (Graph-based, Verifiable Workflow)

Commands:
  falcon init [-n name]         Initialize a new repo
  falcon status                 Show working tree status
  falcon add <path>             Stage files for commit
  falcon reset <path>           Unstage files (undo add)
  falcon commit [-m msg]        Create a new commit from staged files
  falcon log                    Show commit logs in DAG order
  falcon diff <v1> <v2>         Show line-by-line changes
  
  -- Remote & High Availability --
  falcon login <url>            [Integrated] Setup identity and server connection
  falcon trust [url] [code]     Pair a new device using an 8-digit OTP
  falcon push                   Securely upload .fco to server
  falcon pull <url>             Securely clone or update via certificate
  falcon sls                    List your projects on remote server
  falcon rm <project>           Delete a project from remote server
  falcon keygen [server]        Generate device keys and register (Passport)
  falcon regis <user> -t <tok>  Register remote server credentials

  -- Master & Slave (Real-time Sync) --
  falcon set master <br> <url>  Master mode: Auto-sync changes to server
  falcon set slave <br> <url>   Slave mode: Auto-update from server
  falcon unset                  Terminate sync session
  falcon serve [port]           Start a Falcon Sync Server (default: 50005)

  -- Management --
  falcon checkout <target>      Switch branches or restore files
  falcon branch [name]          List or create branches
  falcon merge <branch>         Merge branches
  falcon forget                 Stop tracking files matched by .fignore
  falcon gc                     Garbage collect orphaned blobs (clean cache)
  falcon version                Show Falcon CLI version
  falcon vls (or tree)          Show visual version tree & branches
  falcon user <name>            Change your username/identity
  falcon update                 Update Falcon to the latest version via GitHub
`
	fmt.Print(usage)
}
func handleUpdate() {
	fmt.Println("🔄 Updating Falcon to the latest version...")

	// Use the remote installer to update. This works regardless of whether
	// the user is in a git worktree or has Go installed.
	updateCmd := "curl -sL https://raw.githubusercontent.com/BlueYeoul/Falcon/main/install.sh | bash"

	fmt.Println("� Fetching and running the latest installer from GitHub...")
	cmd := exec.Command("/bin/bash", "-c", updateCmd)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Printf("❌ Update failed: %v\n", err)
		fmt.Println("Please try running the installer manually:")
		fmt.Println("  curl -sL https://raw.githubusercontent.com/BlueYeoul/Falcon/main/install.sh | bash")
		return
	}

	fmt.Println("\n✨ Falcon has been updated successfully!")
}
