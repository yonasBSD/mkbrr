# mkbrr

```
         __   ___.                 
  _____ |  | _\_ |________________
 /     \|  |/ /| __\___ \_  __ \
|  Y Y  \    < | \_\ \  | \/|  | \/
|__|_|  /__|_ \|___  /__|   |__|
      \/     \/    \/
```

mkbrr is a command-line tool to create and inspect torrent files. Fast, single binary, no dependencies. Written in Go.

## Performance

mkbrr is blazingly fast, matching or outperforming other popular torrent creation tools. Here are some benchmarks on Apple Silicon:

### Large File (3.59GB MKV)

```bash
# mktorrent
time mktorrent -p -a https://tracker.com/announce -o "mktorrent.torrent" "episode.mkv"
Duration: 1.35s user 0.49s system 103% cpu 1.780 total

# mkbrr
time mkbrr create episode.mkv -p -v -t https://tracker.com/announce
Duration: 1.26s user 0.46s system 97% cpu 1.760 total
```

### Small Directory (350MB Music Album)

```bash
# mktorrent
time mktorrent -p -a https://tracker.com/announce -o "mktorrent.torrent" "album/"
Duration: 0.13s user 0.06s system 98% cpu 0.201 total

# mkbrr
time mkbrr create 'album/' -p -v -t https://tracker.com/announce
Duration: 0.12s user 0.05s system 90% cpu 0.196 total
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
