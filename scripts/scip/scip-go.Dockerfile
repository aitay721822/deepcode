FROM golang:1.27.1-bookworm AS build
RUN CGO_ENABLED=0 GOTOOLCHAIN=local go install github.com/scip-code/scip-go/cmd/scip-go@v0.2.10

FROM golang:1.27.1-bookworm
COPY --from=build /go/bin/scip-go /usr/local/bin/scip-go
