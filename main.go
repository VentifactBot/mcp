package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

func main() {
	if err := ensureConfigDirs(); err != nil {
		fatal("config setup: %v", err)
	}

	args := os.Args[1:]
	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}

	cmd := args[0]
	cmdArgs := args[1:]

	var err error
	switch cmd {
	case "servers":
		err = cmdServers()
	case "add":
		err = cmdAdd(cmdArgs)
	case "remove":
		err = cmdRemove(cmdArgs)
	case "tools":
		err = cmdTools(cmdArgs)
	case "call":
		err = cmdCall(cmdArgs)
	case "auth":
		err = cmdAuth(cmdArgs)
	case "ping":
		err = cmdPing(cmdArgs)
	case "enable":
		err = cmdSetEnabled(cmdArgs, true)
	case "disable":
		err = cmdSetEnabled(cmdArgs, false)
	case "auth-callback":
		err = cmdAuthCallback(cmdArgs)
	case "help", "--help", "-h":
		printUsage()
	case "version", "--version", "-v":
		fmt.Println("mcp-cli " + Version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		if errors.Is(err, errToolFailed) {
			os.Exit(1)
		}
		fatal("%v", err)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `Usage: mcp <command> [args...]

Commands:
  servers                        List configured servers
  add <name> <url>               Add an HTTP server
  add <name> --stdio <cmd> ...   Add a stdio server
  remove <name>                  Remove a server
  enable <name>                  Enable a server
  disable <name>                 Disable a server
  tools [server] [--query <q>]   List available tools
  call <server> <tool> [flags]   Call a tool
  auth <name> [flags]            Authenticate with a server
  ping <server>                  Ping a server (liveness check)
  auth-callback --nonce ...   Complete OAuth (called by gateway)
  help                           Show this help
  version                        Show version

Call flags:
  --params '{"key":"val"}'       Tool parameters (or pipe via stdin)
  --stream                       Stream progress events as NDJSON
  --max-output N                 Truncate output to N chars (default 30000)

Auth flags:
  --callback-url <url>           Use relay mode (for sprites)
  --agent-id <id>                Agent ID for relay callbacks

Environment:
  MCP_AUTH_TOKEN               Bearer token (use instead of OAuth flow)
  MCP_AUTH_CODE                Authorization code (set by gateway for auth-callback)
  MCP_CLIENT_ID                OAuth client ID (for static client credentials)
  MCP_CLIENT_SECRET            OAuth client secret (for static client credentials)
  MCP_CALLBACK_URL             Default callback URL for relay mode
  MCP_AGENT_ID                 Default agent ID for relay mode`)
}

// cmdServers handles the `mcp servers` command.
func cmdServers() error {
	servers, err := loadServers()
	if err != nil {
		return err
	}
	return outputJSON(servers)
}

// cmdSetEnabled handles `mcp enable <name>` and `mcp disable <name>`.
func cmdSetEnabled(args []string, enabled bool) error {
	if len(args) < 1 {
		if enabled {
			return fmt.Errorf("usage: mcp enable <name>")
		}
		return fmt.Errorf("usage: mcp disable <name>")
	}
	name := args[0]
	if err := validateServerName(name); err != nil {
		return err
	}
	servers, err := loadServers()
	if err != nil {
		return err
	}
	found := false
	for i, s := range servers {
		if s.Name == name {
			servers[i].Enabled = &enabled
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("server %q not found", name)
	}
	if err := saveServers(servers); err != nil {
		return err
	}
	if enabled {
		logStderr("enabled server %q", name)
	} else {
		logStderr("disabled server %q", name)
	}
	return nil
}

// cmdAdd handles the `mcp add` command.
func cmdAdd(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: mcp add <name> <url>  or  mcp add <name> --stdio <command> [args...]")
	}

	name := args[0]
	if err := validateServerName(name); err != nil {
		return err
	}

	if args[1] == "--stdio" {
		if len(args) < 3 {
			return fmt.Errorf("usage: mcp add <name> --stdio <command> [args...]")
		}
		return addServer(ServerConfig{
			Name:      name,
			Transport: "stdio",
			Command:   args[2],
			Args:      args[3:],
		}, "")
	}

	// HTTP mode
	serverURL := args[1]
	if err := validateEndpointURL(serverURL, "MCP server"); err != nil {
		return err
	}
	authToken, _ := getAuthToken(name)
	return addServer(ServerConfig{
		Name:      name,
		Transport: "streamable-http",
		URL:       serverURL,
	}, authToken)
}

func addServer(server ServerConfig, authToken string) error {
	logStderr("connecting to %s...", server.Name)
	tools, err := discoverTools(&server, authToken)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	if err := addServerConfig(server); err != nil {
		return err
	}
	if err := saveCachedTools(server.Name, tools); err != nil {
		logStderr("warning: cache write failed: %v", err)
	}
	logStderr("added server %q (%s) — %d tools discovered", server.Name, server.Transport, len(tools))
	return nil
}

// cmdRemove handles the `mcp remove` command.
func cmdRemove(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: mcp remove <name>")
	}
	name := args[0]
	if err := validateServerName(name); err != nil {
		return err
	}
	if err := removeServerConfig(name); err != nil {
		return err
	}
	logStderr("removed server %q", name)
	return nil
}

// cmdPing handles the `mcp ping <server>` command.
func cmdPing(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: mcp ping <server>")
	}

	if err := validateServerName(args[0]); err != nil {
		return err
	}

	server, err := getServerConfig(args[0])
	if err != nil {
		return err
	}

	authToken, err := getAuthToken(args[0])
	if err != nil {
		logStderr("warning: auth token load failed: %v", err)
	}

	transport, err := mcpConnect(server, authToken)
	if err != nil {
		return err
	}
	defer transport.Close()

	if err := mcpPing(transport); err != nil {
		return err
	}

	return outputJSON(map[string]string{"status": "ok"})
}

// outputJSON writes a value as JSON to stdout.
func outputJSON(v interface{}) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// logStderr writes a formatted message to stderr.
func logStderr(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

// fatal writes an error to stderr and exits.
func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}

