Credentials are stored in `~/.config/pacto/config.json` (or `$XDG_CONFIG_HOME/pacto/config.json`), keeping them separate from Docker's configuration.

### GitHub Container Registry (ghcr.io)

For GitHub registries (`ghcr.io` and `docker.pkg.github.com`), pacto can reuse credentials from the [GitHub CLI](https://cli.github.com/), so you can skip `pacto login`.

If you already have `gh` installed and authenticated, pacto will use `gh auth token` to obtain a token transparently. To verify your setup:

```bash
# Check if gh is authenticated
gh auth status

# Verify the token is available
gh auth token
```

To push container images or packages, your token needs the `write:packages` scope. If you authenticated `gh` without it, refresh your scopes:

```bash
gh auth refresh --scopes write:packages
```

After this, `pacto push oci://ghcr.io/...` will work without any additional login step.

If `gh` is missing or unauthenticated, pacto moves on to the next credential source in the chain.
