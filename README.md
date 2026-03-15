# mcp

A CLI tool for discovering and calling tools on external [MCP](https://modelcontextprotocol.io/) (Model Context Protocol) servers.

## Install

```bash
go install github.com/paulbellamy/mcp@latest
```

## Usage

```bash
# List configured servers
mcp servers

# Add a server (HTTP or stdio)
mcp add <name> <url>
mcp add <name> --stdio <command> [args...]

# Discover tools
mcp tools [server] [--query "search term"] [--refresh]

# Call a tool
mcp call <server> <tool> --params '{"key": "value"}'

# Stream progress for long-running tools
mcp call <server> <tool> --stream --params '{"key": "value"}'

# Authenticate
mcp auth <name> --callback-url <url>

# Ping / remove
mcp ping <server>
mcp remove <name>
```

## License

MIT
