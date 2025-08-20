# 🗂️ SharePoint Catalog

A beautiful, fast, and functional command-line tool for cataloging SharePoint directories into SQLite databases. Built with Go and featuring a stunning terminal user interface powered by Charm's Bubble Tea framework.

![SharePoint Catalog Demo](https://img.shields.io/badge/Terminal-UI-blueviolet?style=for-the-badge&logo=gnometerminal)
![Go Version](https://img.shields.io/badge/Go-1.23+-00ADD8?style=for-the-badge&logo=go)
![SQLite](https://img.shields.io/badge/SQLite-Database-003B57?style=for-the-badge&logo=sqlite)

## ✨ Features

### 🎨 **Beautiful Terminal Interface**
- **Full-screen backgrounds** with professional dark theme
- **Decorative borders** and elegant containers
- **Real-time path validation** with visual indicators (✓/⚠/✗)
- **Responsive design** that adapts to any terminal size

### 🚀 **Smart User Experience**
- **Recent paths history** with numbered shortcuts (1-9)
- **Tab completion** for directory paths
- **Progress estimation** with file counting and time remaining
- **Persistent preferences** - remembers your settings between sessions

### ⚡ **High Performance**
- **Batch processing** - Groups 1000 operations per transaction
- **WAL mode SQLite** - Write-Ahead Logging for concurrent access
- **Prepared statements** - Optimized SQL queries
- **Speed monitoring** - Real-time performance metrics

### 📊 **Comprehensive Cataloging**
- **File metadata** - Path, size, modification time, MIME type
- **Directory structure** - Complete folder hierarchy
- **Optional hashing** - SHA256 checksums for file integrity
- **Extension filtering** - Process only specific file types

## 📦 Installation

### Prerequisites
- Go 1.23 or later
- Terminal with true color support (recommended)

### Build from Source
```bash
git clone <repository-url>
cd SP_Catalog
go build -o spcatalog
```

### Run
```bash
./spcatalog
```

## 🎯 Quick Start

1. **Launch the application**
   ```bash
   ./spcatalog
   ```

2. **Enter your SharePoint path**
   - Type the path or use tab completion
   - Press `1-9` to select from recent paths
   - Press `Ctrl+B` to browse directories

3. **Configure options**
   - Set output directory (optional)
   - Add extension filters like `.pdf,.docx,.xlsx`
   - Toggle hash calculation with `Space`

4. **Start cataloging**
   - Press `Enter` to begin
   - Watch real-time progress with percentage and time remaining
   - Press `q` to safely stop (preserves database)

## 📋 Usage Examples

### Basic Cataloging
```bash
# Catalog entire SharePoint directory
Root path: /Users/you/OneDrive/SharePoint
Output dir: /Users/you/spcatalog
```

### With Extension Filter
```bash
# Only catalog PDF and Office documents
Root path: /Users/you/OneDrive/SharePoint
Ext filter: .pdf,.docx,.xlsx,.pptx
```

### With Hash Calculation
```bash
# Include SHA256 checksums (slower but adds integrity)
Root path: /Users/you/OneDrive/SharePoint
Hash: on  (toggle with Space)
```

## 🎹 Keyboard Shortcuts

### Form Screen
| Key | Action |
|-----|--------|
| `Enter` | Start cataloging |
| `Tab` | Auto-complete paths |
| `↑/↓` | Navigate fields |
| `1-9` | Select recent paths |
| `Space` | Toggle hash calculation |
| `Ctrl+B` | Open directory browser |
| `?` | Show help |
| `q/ESC` | Quit |

### Directory Browser
| Key | Action |
|-----|--------|
| `↑/↓` or `j/k` | Navigate |
| `Enter` | Enter directory |
| `Space` | Select current directory |
| `ESC` | Return to form |

### Scanning Progress
| Key | Action |
|-----|--------|
| `q/ESC` | Stop scanning (safe) |
| `Ctrl+C` | Force stop |

## 📊 Database Schema

The application creates a SQLite database with two main tables:

### Files Table
```sql
CREATE TABLE files (
    abs_path    TEXT PRIMARY KEY,
    folder_path TEXT NOT NULL,
    name        TEXT NOT NULL,
    ext         TEXT,
    size        INTEGER,
    mtime_utc   TEXT,
    mime        TEXT,
    sha256      TEXT
);
```

### Folders Table
```sql
CREATE TABLE folders (
    path TEXT PRIMARY KEY,
    parent_path TEXT,
    mtime_utc TEXT
);
```

## 🔍 Querying Your Data

### Example SQLite Queries

**Find all PDF files:**
```sql
SELECT * FROM files WHERE ext = '.pdf';
```

**Count files by folder:**
```sql
SELECT folder_path, COUNT(*) as file_count 
FROM files 
GROUP BY folder_path 
ORDER BY file_count DESC;
```

**Find large files (>100MB):**
```sql
SELECT name, size, folder_path 
FROM files 
WHERE size > 100000000 
ORDER BY size DESC;
```

**Files by extension:**
```sql
SELECT ext, COUNT(*) as count, SUM(size) as total_size 
FROM files 
GROUP BY ext 
ORDER BY count DESC;
```

**Recent files (last 30 days):**
```sql
SELECT name, folder_path, mtime_utc 
FROM files 
WHERE datetime(mtime_utc) > datetime('now', '-30 days') 
ORDER BY mtime_utc DESC;
```

## ⚡ Performance

Typical performance ranges:
- **Local SSD**: 200-500+ files/second
- **Local HDD**: 50-150 files/second
- **OneDrive (synced)**: 100-300 files/second
- **Network drives**: 20-100 files/second

Performance factors:
- **Storage type** (biggest impact)
- **Hash calculation** (~50% slower when enabled)
- **File count vs size** (many small files are faster per file)
- **System resources** (RAM, CPU, I/O bandwidth)

## 🔧 Configuration

Settings are automatically saved to `~/.spcatalog_config.json`:

```json
{
  "recent_paths": [
    "/Users/you/OneDrive/SharePoint",
    "/Users/you/Documents/Projects"
  ],
  "max_recent": 9,
  "last_root_path": "/Users/you/OneDrive/SharePoint",
  "last_output_dir": "/Users/you/spcatalog",
  "last_ext_filter": ".pdf,.docx,.xlsx",
  "last_hash_setting": false
}
```

## 🛠️ Technical Details

### Dependencies
- **Bubble Tea** - Terminal user interface framework
- **Lipgloss** - Style definitions and layout
- **Bubbles** - UI components (textinput, spinner)
- **modernc.org/sqlite** - Pure Go SQLite driver

### Architecture
- **Model-View-Update (MVU)** pattern via Bubble Tea
- **State machines** for different application screens
- **Concurrent processing** with progress reporting
- **Batch database operations** for performance
- **WAL mode SQLite** for safety and concurrency

### Safety Features
- **WAL journaling** - Safe interruption without data loss
- **Prepared statements** - SQL injection protection
- **Error handling** - Graceful failure recovery
- **Transaction batching** - Atomic operations

## 📝 License

This project is licensed under the MIT License - see the LICENSE file for details.

## 🤝 Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## 📞 Support

If you encounter any issues or have questions, please open an issue on GitHub.

---

**Made with ❤️ and Go** | *Fast • Beautiful • Functional*