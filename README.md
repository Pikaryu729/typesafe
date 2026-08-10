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

## Development

```sh
make build    # build the server binary
make run      # run the server
make test     # go test ./... -race
make lint     # gofmt check + go vet
```
