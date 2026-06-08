package main

import "fmt"

// ─── Workflow templates ───────────────────────────────────────────────────────

func backendWorkflow(projectName string) string {
	return fmt.Sprintf(`name: Build and Deploy Cuento

on:
  push:
    branches: [release]
    paths-ignore:
      - 'deployments/**'
  workflow_dispatch:

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout code
        uses: actions/checkout@v4

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.25'
          cache: true

      - name: Build Binary
        run: GOOS=linux GOARCH=amd64 go build -o cuento-backend main.go

      - name: Copy binary to server
        uses: appleboy/scp-action@v0.1.7
        with:
          host: ${{ secrets.DROPLET_IP }}
          username: root
          key: ${{ secrets.SSH_PRIVATE_KEY }}
          source: "cuento-backend"
          target: "/var/www/backend"

      - name: Copy locales to server
        uses: appleboy/scp-action@v0.1.7
        with:
          host: ${{ secrets.DROPLET_IP }}
          username: root
          key: ${{ secrets.SSH_PRIVATE_KEY }}
          source: "locales"
          target: "/var/www/backend"

      - name: Restart service
        uses: appleboy/ssh-action@v1.0.3
        with:
          host: ${{ secrets.DROPLET_IP }}
          username: root
          key: ${{ secrets.SSH_PRIVATE_KEY }}
          script: |
            chmod +x /var/www/backend/cuento-backend
            grep -v '^PROJECT_NAME=' /etc/environment > /tmp/env.tmp && mv /tmp/env.tmp /etc/environment || true
            echo 'PROJECT_NAME=%s' >> /etc/environment
            systemctl restart cuento-backend
`, projectName)
}

// devWorkflowDisabled replaces the upstream dev pipeline in a fork so that
// writes to main (e.g. workflow resets) do not trigger unintended dev deploys.
const devWorkflowDisabled = `name: Dev pipeline (disabled in fork)

on:
  workflow_dispatch:

jobs:
  disabled:
    runs-on: ubuntu-latest
    steps:
      - name: Disabled
        run: echo "Dev pipeline is disabled in this fork. Use the release branch."
`

func frontendWorkflow(projectName string) string {
	return fmt.Sprintf(`name: Deploy Cuento Frontend

on:
  push:
    branches: [release]
    paths-ignore:
      - 'deployments/**'
  workflow_dispatch:

jobs:
  build-and-deploy:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout code
        uses: actions/checkout@v4

      - name: Set up Node.js
        uses: actions/setup-node@v4
        with:
          node-version: '20'
          cache: 'npm'

      - name: Install dependencies
        run: npm ci

      - name: Build Angular app
        run: npm run build

      - name: Clear old files (preserving custom styles and uploads)
        uses: appleboy/ssh-action@v1.0.3
        with:
          host: ${{ secrets.DROPLET_IP }}
          username: root
          key: ${{ secrets.SSH_PRIVATE_KEY }}
          script: |
            find /var/www/frontend -mindepth 1 \
              ! -name 'favicon*' \
              ! -name 'main_style*' \
              ! -name 'custom_style*' \
              ! -path '/var/www/frontend/reactions' \
              ! -path '/var/www/frontend/reactions/*' \
              -delete
            mkdir -p /var/www/frontend/reactions

      - name: Deploy to temporary staging
        uses: appleboy/scp-action@v0.1.7
        with:
          host: ${{ secrets.DROPLET_IP }}
          username: root
          key: ${{ secrets.SSH_PRIVATE_KEY }}
          source: "dist/cuento-frontend/browser/*"
          target: "/tmp/frontend_staging"
          strip_components: 3
          overwrite: true

      - name: Move to target and fix permissions
        uses: appleboy/ssh-action@v1.0.3
        with:
          host: ${{ secrets.DROPLET_IP }}
          username: root
          key: ${{ secrets.SSH_PRIVATE_KEY }}
          script: |
            cp -rn /tmp/frontend_staging/* /var/www/frontend/
            chown -R www-data:www-data /var/www/frontend
            chmod -R 775 /var/www/frontend
            rm -rf /tmp/frontend_staging
            grep -v '^PROJECT_NAME=' /etc/environment > /tmp/env.tmp && mv /tmp/env.tmp /etc/environment || true
            echo 'PROJECT_NAME=%s' >> /etc/environment
`, projectName)
}

// ─── Sonic workflow ───────────────────────────────────────────────────────────

const sonicWorkflow = `name: Install Sonic

on:
  workflow_dispatch:

jobs:
  install-sonic:
    runs-on: ubuntu-22.04
    steps:
      - name: Checkout
        uses: actions/checkout@v4

      - name: Install Rust and build Sonic
        run: |
          sudo apt-get install -y build-essential git
          curl https://sh.rustup.rs -sSf | sh -s -- -y --profile minimal
          source "$HOME/.cargo/env"
          git clone --depth 1 https://github.com/valeriansaliou/sonic.git sonic-src
          cd sonic-src
          cargo build --release
          mkdir -p ../deploy
          cp target/release/sonic ../deploy/sonic
          cp ../.github/sonic/sonic.cfg ../deploy/sonic.cfg
          cp ../.github/sonic/sonic.service ../deploy/sonic.service

      - name: Copy files to server
        uses: appleboy/scp-action@v0.1.7
        with:
          host: ${{ secrets.DROPLET_IP }}
          username: root
          key: ${{ secrets.SSH_PRIVATE_KEY }}
          source: "deploy/*"
          target: "/tmp/sonic-deploy/"
          strip_components: 1

      - name: Install and start Sonic
        uses: appleboy/ssh-action@v1.0.3
        with:
          host: ${{ secrets.DROPLET_IP }}
          username: root
          key: ${{ secrets.SSH_PRIVATE_KEY }}
          script: |
            cp /tmp/sonic-deploy/sonic /usr/local/bin/sonic
            chmod 755 /usr/local/bin/sonic
            cp /tmp/sonic-deploy/sonic.cfg /etc/sonic.cfg
            cp /tmp/sonic-deploy/sonic.service /etc/systemd/system/sonic.service
            id -u sonic &>/dev/null || useradd --system --no-create-home --shell /usr/sbin/nologin sonic
            mkdir -p /var/lib/sonic/store/kv /var/lib/sonic/store/fst
            chown -R sonic:sonic /var/lib/sonic
            systemctl daemon-reload
            systemctl enable sonic
            systemctl restart sonic
            rm -rf /tmp/sonic-deploy
`

const sonicCfgFile = `[server]
log_level = "error"

[channel]
inet = "127.0.0.1:1491"
tcp_timeout = 300
auth_password = "SecretPassword"

[channel.search]
query_limit_default = 10
query_limit_maximum = 100
query_alternates_try = 4
suggest_limit_default = 5
suggest_limit_maximum = 20
list_limit_default = 100
list_limit_maximum = 500

[store]

[store.kv]
path = "/var/lib/sonic/store/kv/"
retain_word_objects = 1000

[store.kv.pool]
inactive_after = 1800

[store.kv.database]
flush_after = 900
compress = true
parallelism = 2
max_files = 100
max_compactions = 1
max_flushes = 1
write_buffer = 16384
write_ahead_log = true

[store.fst]
path = "/var/lib/sonic/store/fst/"

[store.fst.pool]
inactive_after = 300

[store.fst.graph]
consolidate_after = 180
max_size = 2048
max_words = 250000
`

const sonicServiceFile = `[Unit]
Description=Sonic Search Index
After=network.target

[Service]
Type=simple
User=sonic
ExecStart=/usr/local/bin/sonic -c /etc/sonic.cfg
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
`

// ─── Qdrant workflow ──────────────────────────────────────────────────────────

const qdrantWorkflow = `name: Install Qdrant

on:
  workflow_dispatch:

jobs:
  install-qdrant:
    runs-on: ubuntu-22.04
    steps:
      - name: Checkout
        uses: actions/checkout@v4

      - name: Download Qdrant
        run: |
          QDRANT_VERSION=$(curl -s https://api.github.com/repos/qdrant/qdrant/releases/latest | grep '"tag_name"' | cut -d'"' -f4)
          curl -L "https://github.com/qdrant/qdrant/releases/download/${QDRANT_VERSION}/qdrant-x86_64-unknown-linux-musl.tar.gz" -o qdrant.tar.gz
          tar -xzf qdrant.tar.gz
          mkdir -p deploy
          cp qdrant deploy/qdrant
          cp .github/qdrant/qdrant.service deploy/qdrant.service

      - name: Copy files to server
        uses: appleboy/scp-action@v0.1.7
        with:
          host: ${{ secrets.DROPLET_IP }}
          username: root
          key: ${{ secrets.SSH_PRIVATE_KEY }}
          source: "deploy/*"
          target: "/tmp/qdrant-deploy/"
          strip_components: 1

      - name: Install and start Qdrant
        uses: appleboy/ssh-action@v1.0.3
        with:
          host: ${{ secrets.DROPLET_IP }}
          username: root
          key: ${{ secrets.SSH_PRIVATE_KEY }}
          script: |
            cp /tmp/qdrant-deploy/qdrant /usr/local/bin/qdrant
            chmod 755 /usr/local/bin/qdrant
            cp /tmp/qdrant-deploy/qdrant.service /etc/systemd/system/qdrant.service
            id -u qdrant &>/dev/null || useradd --system --no-create-home --shell /usr/sbin/nologin qdrant
            mkdir -p /var/lib/qdrant/storage /var/lib/qdrant/snapshots
            chown -R qdrant:qdrant /var/lib/qdrant
            systemctl daemon-reload
            systemctl enable qdrant
            systemctl restart qdrant
            rm -rf /tmp/qdrant-deploy
`

const qdrantServiceFile = `[Unit]
Description=Qdrant Vector Search Engine
After=network.target

[Service]
Type=simple
User=qdrant
WorkingDirectory=/var/lib/qdrant
ExecStart=/usr/local/bin/qdrant
Restart=on-failure
RestartSec=5
Environment=QDRANT__STORAGE__STORAGE_PATH=/var/lib/qdrant/storage
Environment=QDRANT__STORAGE__ON_DISK_PAYLOAD=true

[Install]
WantedBy=multi-user.target
`
