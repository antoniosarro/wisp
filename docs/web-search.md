# Setting up web search

wisp's `web_search` tool needs a search endpoint. The usual one is a
[SearXNG](https://docs.searxng.org/) instance on your own machine: it needs
no account or key, and it asks several search engines at once. Brave
Search's API is the hosted alternative.

Without an endpoint the tool doesn't exist for the model; with one, it
appears in every session:

```sh
export WISP_SEARCH_URL=http://127.0.0.1:8888   # or: wisp --search-url http://127.0.0.1:8888
```

What the tool returns, and how it asks before searching, is in
[tools.md](tools.md#searching-the-web-web_search).

- [The settings SearXNG needs](#the-settings-searxng-needs)
- [Docker, on any distribution](#docker-on-any-distribution)
- [NixOS](#nixos)
- [Ubuntu and Debian](#ubuntu-and-debian)
- [Arch Linux](#arch-linux)
- [Brave Search instead](#brave-search-instead)
- [Checking it works](#checking-it-works)
- [Problems](#problems)

## The settings SearXNG needs

Every setup below gives SearXNG the same three settings:

```yaml
use_default_settings: true
server:
  secret_key: "change-me"   # any long random string
  limiter: false            # a private instance needs no rate limiting
search:
  formats: [html, json]     # json is what wisp reads
```

The one people forget is `formats`: SearXNG answers only in HTML by
default, and refuses wisp's requests with a 403 until `json` is listed.
wisp says so when it happens.

Listen on `127.0.0.1` only, as every example here does: with
`limiter: false`, an instance open to the network can be used by anyone
who finds it.

## Docker, on any distribution

This works the same on NixOS, Ubuntu and Arch once Docker runs (see each
section below for installing it).

1. Write the settings, with a random secret key:

   ```sh
   mkdir -p ~/.config/searxng
   cat > ~/.config/searxng/settings.yml <<EOF
   use_default_settings: true
   server:
     secret_key: "$(head -c 32 /dev/urandom | base64 | tr -d '/+=')"
     limiter: false
   search:
     formats: [html, json]
   EOF
   ```

2. Start the container:

   ```sh
   docker run -d --name searxng --restart unless-stopped \
     -p 127.0.0.1:8888:8080 \
     -v ~/.config/searxng:/etc/searxng:ro \
     docker.io/searxng/searxng:latest
   ```

   - `--restart unless-stopped` starts it again after a crash or a reboot,
     as long as Docker itself starts at boot.
   - `:ro` mounts the settings read-only. Without it the container takes
     ownership of the folder, and you can no longer edit the file. Its log
     then shows `chown: ... Read-only file system`, which is harmless.

3. After editing `settings.yml`: `docker restart searxng`. To update:
   `docker pull docker.io/searxng/searxng:latest`, `docker rm -f searxng`,
   and step 2 again.

## NixOS

Two ways, both declarative. Both were checked to evaluate against NixOS
25.11.

**Native, with the `services.searx` module** (no container). The secret
key comes from a file, so it stays out of the Nix store; with sops-nix,
point `environmentFile` at the secret.

```nix
services.searx = {
  enable = true;
  environmentFile = "/run/secrets/searx"; # holds SEARX_SECRET_KEY=...
  settings = {
    server.bind_address = "127.0.0.1";
    server.port = 8888;
    server.secret_key = "$SEARX_SECRET_KEY";
    server.limiter = false;
    search.formats = [ "html" "json" ];
  };
};
```

It runs as the `searx` systemd service: `systemctl status searx`,
`journalctl -u searx`.

**The container, declared** with `virtualisation.oci-containers`, if you'd
rather run the upstream image:

```nix
virtualisation.docker.enable = true;          # or podman, with backend = "podman"
virtualisation.oci-containers.backend = "docker";
virtualisation.oci-containers.containers.searxng = {
  image = "docker.io/searxng/searxng:latest";
  ports = [ "127.0.0.1:8888:8080" ];
  volumes = [ "/etc/searxng:/etc/searxng:ro" ];
};
environment.etc."searxng/settings.yml".text = ''
  use_default_settings: true
  server:
    secret_key: "change-me"   # in the Nix store, readable by every user
    limiter: false
  search:
    formats: [html, json]
'';
```

It runs as the `docker-searxng` systemd service. The settings file is in
the Nix store this way, so its secret key is world-readable: fine for an
instance on localhost, otherwise use the native module.

Then set the variable for your user, for example with home-manager:

```nix
home.sessionVariables.WISP_SEARCH_URL = "http://127.0.0.1:8888";
```

## Ubuntu and Debian

Install Docker and let it start at boot:

```sh
sudo apt install docker.io
sudo systemctl enable --now docker
sudo usermod -aG docker "$USER"   # then log out and in, to run docker without sudo
```

Then follow [Docker, on any distribution](#docker-on-any-distribution), and
add the variable to `~/.bashrc` (or `~/.zshrc`):

```sh
echo 'export WISP_SEARCH_URL=http://127.0.0.1:8888' >> ~/.bashrc
```

SearXNG also has a native installation script for Ubuntu and Debian
([docs.searxng.org](https://docs.searxng.org/admin/installation-scripts.html)),
which sets up uWSGI and a web server. Docker is less to maintain for a
single local user.

## Arch Linux

```sh
sudo pacman -S docker
sudo systemctl enable --now docker
sudo usermod -aG docker "$USER"   # then log out and in
```

Then [Docker, on any distribution](#docker-on-any-distribution), and add
the variable to your shell's startup file:

```sh
echo 'export WISP_SEARCH_URL=http://127.0.0.1:8888' >> ~/.zshrc
```

## Brave Search instead

No server to run, but you need an API key from
[brave.com/search/api](https://brave.com/search/api/), and every query goes
to Brave:

```sh
export WISP_SEARCH_URL=https://api.search.brave.com/res/v1/web/search
export WISP_SEARCH_KEY=...   # better: read it from a file, e.g. $(< ~/.config/wisp/brave-key)
```

wisp refuses to start with Brave's URL and no key.

## Checking it works

SearXNG answering in JSON, which is what wisp needs:

```sh
curl -s 'http://127.0.0.1:8888/search?q=nixos&format=json' | head -c 300
```

A list of results means it works. Then in wisp, ask something that needs
the web; the tool card shows `web_search` with the query, and the result
lists titles, URLs and snippets.

## Problems

- **"SearXNG refused to answer in JSON: add json to search.formats"**:
  `formats: [html, json]` is missing from the settings, or SearXNG wasn't
  restarted after the change.
- **"connection refused"**: SearXNG isn't running, or listens on another
  port. `docker ps`, `docker logs searxng`, or `systemctl status searx`.
- **No results, or very few**: some engines block automated searches for a
  while. SearXNG asks others; try again later, or enable more engines in
  its settings.
- **`ahmia` or `torch` "can't register engine" in the log**: those engines
  need Tor. SearXNG skips them; the others work.
- **The port is taken**: use another one in both places, e.g.
  `-p 127.0.0.1:8890:8080` and `WISP_SEARCH_URL=http://127.0.0.1:8890`.
