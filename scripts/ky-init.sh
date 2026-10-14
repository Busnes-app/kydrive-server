#!/usr/bin/env bash
set -euo pipefail

# ky-init.sh - Scaffold a new Busnes.app product from kydrive-server

if [ "$#" -lt 1 ]; then
    echo "Usage: $0 <app-name> [target-directory]"
    echo "Example: $0 kydrive-server ../kydrive-server"
    exit 1
fi

APP_NAME="$1"
if [[ ! $APP_NAME =~ ^[a-z0-9][a-z0-9-]*$ ]]; then
    echo "❌ Error: app name must be lowercase letters, digits and hyphens, starting with a letter or digit."
    exit 1
fi
TARGET_DIR="${2:-../$APP_NAME}"
BASE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

echo "🚀 Initializing new Busnes.app product: ${APP_NAME}..."
echo "📂 Target Directory: ${TARGET_DIR}"

if [ -d "$TARGET_DIR" ] && [ "$(ls -A "$TARGET_DIR" 2>/dev/null)" ]; then
    echo "❌ Error: Target directory $TARGET_DIR already exists and is not empty."
    exit 1
fi

mkdir -p "$TARGET_DIR"

# Copy the committed tree only: local data, secrets, builds and capsules never leave the source.
git -C "$BASE_DIR" archive HEAD | tar -x -C "$TARGET_DIR"

cd "$TARGET_DIR"

MODULE_OLD="$(cd "$BASE_DIR" && go list -m)"
MODULE_NEW="github.com/Busnes-app/${APP_NAME}"

echo "🔄 Updating Go module paths (${MODULE_OLD} -> ${MODULE_NEW})..."
find . -type f \( -name "*.go" -o -name "go.mod" -o -name "*.md" \) -exec sed -i "s|${MODULE_OLD}|${MODULE_NEW}|g" {} +

echo "🎨 Updating application branding & configuration..."
# Update package.json name
if [ -f "web/package.json" ]; then
    sed -i "s|\"name\": \"kydrive-server-web\"|\"name\": \"${APP_NAME}-web\"|g" web/package.json
fi

# Update Docker Compose container name
if [ -f "docker-compose.yml" ]; then
    sed -i "s|container_name: kydrive-server|container_name: ${APP_NAME}|g" docker-compose.yml
fi

echo "📦 Running go mod tidy..."
go mod tidy

echo "✨ Successfully scaffolded ${APP_NAME} in ${TARGET_DIR}!"
echo ""
echo "To get started:"
echo "  cd ${TARGET_DIR}"
echo "  make all"
echo "  ./${APP_NAME}"
