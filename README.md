# typesafe

An SSH-accessible typing-practice and typing-race application. Connect over SSH, get a
terminal UI, practice your typing or race other connected users head-to-head on the same
passage.

## Running

```sh
go run ./cmd/server
```

Then, from another terminal:

```sh
ssh -p 2222 yourname@localhost
```

Any public key is accepted; the username you connect with becomes your display name.

## What you can do

**Practice** — type a generated 30-word passage at your own pace, with per-character feedback
and live WPM and accuracy. The clock starts on your first keystroke.

**Race** — create a lobby or join one from the browser (or by its four-character code), ready
up, and race everyone else on the same passage. You see opponents' progress bars move in real
time. Everyone is timed from the same instant, so hesitating at the start costs you. The host
can call a rematch on a fresh passage.

Keys are shown at the bottom of every screen. `ctrl+c` disconnects from anywhere.

## Development

```sh
make build    # build the server binary
make run      # run the server
make test     # go test ./... -race
make lint     # gofmt check + go vet
```
