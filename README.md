# git-sync

Tiny API to trigger local git repositories to sync with remote

## How it works

For each repository you want to control, you'll need the following information:

- path to the repository on disk
- clone URL of the repository
- branch to sync with
- one or more strings to use as API tokens

To sync a repository, you make a POST request to the server with a simple payload, setting the `X-GIT-SYNC-TOKEN`,
and the server will wipe away any local changes at that path and hard sync it to the branch you specified.

An example request:

`curl -X POST -d '{"name": "module-logger"}' -H "X-GIT-SYNC-TOKEN: batman"`

The `name` value matches the name of the configured repository.

## Installation

Download the latest release, in this example, `v1.0.0`:

`curl -o /usr/local/sbin/git-sync https://github.com/uoracs/git-sync/releases/download/v1.0.0/git-sync-v1.0.0-linux-amd64`

Make it executable:

`chmod +x /usr/local/sbin/git-sync`

## Configuration

git-sync requires a YAML config file to run. By default, this file is stored at 
`/etc/git-sync/config.yaml`, and you can specify a different path by setting
`GIT_SYNC_CONFIG_PATH=/path/to/your/config.yaml`.

Example config:

```yaml
---
address: 0.0.0.0
port: 8654
global_tokens: 
    - zelda
repositories:
  - name: module-logger
    local: /opt/module-logger
    remote: https://github.com/uoracs/module-logger.git
    branch: main
    tokens: 
      - batman
  - name: neovim
    local: /opt/neovim
    remote: https://github.com/neovim/neovim.git
    branch: main
    tokens: 
      - spiderman
```

### Global Config

#### address

The listen address for the server.

Default: ""

#### port

The listen port for the server.

Default: 8654

#### global_tokens

Any authentication tokens specified here will be added to the tokens list of all the repositories.

Default: []

### Repository Config

#### name

The name of the repository. This can be any string, and will be the name you provide
when crafting the POST request body.

#### local

Path to the local repository on disk.

#### branch

Branch to sync against.

Default: main

#### tokens

Authentication tokens specific to the repository.

Default: []

## (Optional) Systemd

There is a sample service file in extras/systemd. To install, copy that file to
`/etc/systemd/system/git-sync.service` and edit it for any adjustments to your system, 
such as the location of the binary, or the path to your config file.

Run `systemctl daemon-reload`, then `systemctl enable --now git-sync` to run the service.
