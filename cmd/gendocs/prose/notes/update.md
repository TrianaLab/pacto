!!! note
    `pacto update` works only on release builds. If you built from source without version injection, install a release build first.

### Update notifications

When a newer version is available, pacto displays a notification after any command:

```
A new version of pacto is available: v1.0.0 -> v1.2.0
Run 'pacto update' to update.
```

The check runs asynchronously and adds no latency. Results are cached for 24 hours in `~/.config/pacto/update-check.json`.

Notifications are suppressed when:

- Running a dev build
- Using `--output-format json` or `--output-format markdown` (the notice shows only for text output)
- The `PACTO_NO_UPDATE_CHECK=1` environment variable is set
