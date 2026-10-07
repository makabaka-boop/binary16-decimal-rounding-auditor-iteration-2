# Multi-stage build: the whole toolchain is Go + the standard library
# (math/big provides the exact integer/rational arithmetic), so the
# final image is a static scratch binary.
FROM golang:1.23-bookworm AS build

WORKDIR /src

# Resolve modules first so dependency layers cache independently of
# source edits.
COPY go.mod ./
RUN go mod download

COPY . .

# Run the test suite as part of the image build: the exhaustive round
# trip over every finite binary16 pattern is the correctness gate.
RUN go test ./... && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath \
        -ldflags='-s -w' -o /out/halfconv ./cmd/halfconv

FROM scratch

# Run as a non-root uid; the binary only reads stdin/a file and writes
# stdout, so it needs nothing else.
COPY --from=build /etc/passwd /etc/passwd
USER 65532:65532

COPY --from=build /out/halfconv /usr/local/bin/halfconv

ENTRYPOINT ["/usr/local/bin/halfconv"]
