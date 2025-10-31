#!/bin/bash

# Define the platforms you want to build for
# Format: "OS/ARCH"
targets=(
    "windows/amd64"
    "linux/amd64"
    "linux/arm64"    # For Raspberry Pi / ARM servers
    "darwin/amd64"   # For Intel-based macOS
    "darwin/arm64"   # For Apple Silicon (M1/M2/M3)
)

# Create a directory for the builds
mkdir -p ./build

for target in "${targets[@]}"; do
    # Split the target string into OS and ARCH
    IFS='/' read -r GOOS GOARCH <<< "$target"
    
    echo "Building for $GOOS / $GOARCH..."

    # --- Build Endpoint ---
    ext_endpoint=""
    if [ "$GOOS" = "windows" ]; then
        ext_endpoint=".exe"
    fi
    output_endpoint="./build/${GOOS}-${GOARCH}${ext_endpoint}"
    
    # Build the binary
    GOOS=$GOOS GOARCH=$GOARCH go build -o $output_endpoint ./main.go
done

echo "All builds complete. Binaries are in the ./build directory."