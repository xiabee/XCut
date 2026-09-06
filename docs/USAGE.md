# XCut Command Reference

_Generated from `xcut <command> --help` output (commit b27138a)._

Global flags: `--config`, `--workspace`, `-v`, `-q`, `--help`.

## xcut version
```
usage: xcut version

print build information
```

## xcut config
```
usage: xcut config show|path

show or validate configuration
```

## xcut init
```
usage: xcut init

create workspace layout and default config
```

## xcut doctor
```
usage: xcut doctor

check environment (ffmpeg, workspace, disk, db, optional workers)
```

## xcut cleanup
```
usage: xcut cleanup [--dry-run]

remove temp files and evict analysis cache to budget
```

## xcut project
```
usage: xcut project create|list|show|delete <name>

manage projects
```

## xcut import
```
usage: xcut import <project> <file...>

probe media files into a project
```

## xcut analyze
```
usage: xcut analyze <project> [assetID...]

run baseline analyzers over project assets
```

## xcut timeline
```
usage: xcut timeline <project> [--style name]

generate a timeline for a project
```

## xcut render
```
usage: xcut render <project> [--out path]

render a project timeline to MP4
```

## xcut auto
```
usage: xcut auto <file...> [--style name] [--project name] [--out path]

one-shot: import → analyze → timeline → render
```

## xcut serve
```
usage: xcut serve [--addr host:port]

run the local HTTP API
```

## xcut jobs
```
usage: xcut jobs <project>

list jobs of a project
```

