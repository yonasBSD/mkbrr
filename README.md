# mkbrr

```
         __   ___.                 
  _____ |  | _\_ |________________ 
 /     \|  |/ /| __ \_  __ \_  __ \
|  Y Y  \    < | \_\ \  | \/|  | \/
|__|_|  /__|_ \|___  /__|   |__|   
      \/     \/    \/              
```

mkbrr is a command-line tool to create and inspect torrent files. Fast, single binary, no dependencies. Written in Go.

## Performance

mkbrr is blazingly fast, matching, and sometimes outperforming other popular torrent creation tools. Here are some benchmarks:

### 76GB Remux (Single File) [Ryzen 5 3600 / HDD]

```bash
# mktorrent
time mktorrent -p
Duration: 98.45s user 41.83s system 51% cpu 4:32.48 total

# mkbrr
time mkbrr create -p
Duration: 74.16s user 36.52s system 56% cpu 3:17.26 total
```

### 3.6GB Episode (Single File) [Apple Silicon M3 / NVME]

```bash
# mktorrent
time mktorrent -p
Duration: 1.34s user 0.49s system 103% cpu 1.766 total

# mkbrr
time mkbrr create -p
Duration: 1.27s user 0.67s system 122% cpu 1.587 total
```

### 350MB Music Album (15 Files) [Apple Silicon M3 / NVME]

```bash
# mktorrent
time mktorrent -p
Duration: 0.14s user 0.06s system 96% cpu 0.201 total

# mkbrr
time mkbrr create -p
Duration: 0.13s user 0.05s system 94% cpu 0.189 total
```

## Installation

### Prebuilt Binaries

Download the latest release from the [releases page](https://github.com/autobrr/mkbrr/releases).

### Go Install

If you have Go installed:

```bash
go install github.com/autobrr/mkbrr@latest
```

### Build from Source

Requirements:

- Go 1.23.4 or later

```bash
# Clone the repository
git clone https://github.com/autobrr/mkbrr.git
cd mkbrr

# Build the binary to ./build/mkbrr
make build

# Install the binary to $GOPATH/bin
make install

# Or install system-wide (requires sudo)
sudo make install    # installs to /usr/local/bin

# Other available commands:
make test         # Run tests
make lint         # Run golangci-lint
make clean        # Remove build artifacts
make help         # Show all available commands
```

The build process will automatically include version information and build time in the binary. The version is determined from git tags, defaulting to "dev" if no tags are found.

## Usage

### Create a Torrent

```bash
mkbrr create <path> [flags]
```

#### Create Flags

- `-t, --tracker <url>`: Tracker URL
- `-w, --web-seed <url>`: Add web seed URLs (can be specified multiple times)
- `-p, --private`: Make torrent private
- `-c, --comment <text>`: Add comment
- `-l, --piece-length <n>`: Set piece length to 2^n bytes (14-24, automatic if not specified)
- `-o, --output <path>`: Set output path (default: <name>.torrent)
- `-n, --name <name>`: Set torrent name (default: basename of target)
- `-s, --source <text>`: Add source string
- `-d, --no-date`: Don't write creation date
- `-v, --verbose`: Be verbose

### Inspect a Torrent

```bash
mkbrr inspect <torrent-file>
```

The inspect command displays detailed information about a torrent file, including:

- Name and size
- Number of pieces and piece length
- Private flag status
- Info hash
- Tracker URL(s)
- Creation information
- Magnet link
- File list (for multi-file torrents)

### Version Information

```bash
mkbrr version
```

Displays the version and build time of mkbrr.

## License

This program is free software; you can redistribute it and/or modify it under the terms of the GNU General Public License as published by the Free Software Foundation; either version 2 of the License, or (at your option) any later version.

See [LICENSE](LICENSE) for the full license text.
