#!/bin/sh
set -e

# Run both servers; if either exits the container exits too.
./stella-mcp &
MCP_PID=$!

./stella &
WEB_PID=$!

# Forward SIGTERM/SIGINT to both children.
trap 'kill $MCP_PID $WEB_PID 2>/dev/null' TERM INT

wait $MCP_PID $WEB_PID
