#!/bin/bash
# Installs the Node.js LTS from NodeSource. Debian's own nodejs package lags
# behind (trixie ships 20, past end-of-life) and some frontend tooling needs 22+.
#
# Usage (as root):
#   source node.sh
#   node_ensure

NODE_MAJOR=24

node_ensure() {
    if command -v node >/dev/null 2>&1 \
        && [ "$(node -p "process.versions.node.split('.')[0]")" -ge "$NODE_MAJOR" ]; then
        return 0
    fi
    echo -e "\n⬇️  Installing Node.js $NODE_MAJOR from NodeSource..."
    # Purge Debian's Node and npm (and their orphaned deps) before switching to NodeSource.
    APT_LISTCHANGES_FRONTEND=none apt purge -y nodejs npm || true
    APT_LISTCHANGES_FRONTEND=none apt autoremove -y
    curl -fsSL "https://deb.nodesource.com/setup_${NODE_MAJOR}.x" | bash -
    APT_LISTCHANGES_FRONTEND=none apt install -y nodejs
    echo "    Node $(node -v), npm $(npm -v)"
}
