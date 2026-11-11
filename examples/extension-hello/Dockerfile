FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /hello .

# OpenPlex runs the container as its own uid with a read-only root
# filesystem; /tmp and /data are writable.
FROM scratch
COPY --from=build /hello /hello
ENTRYPOINT ["/hello"]
