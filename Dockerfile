FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/pr0sauce ./cmd/pr0sauce

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/pr0sauce /pr0sauce
ENTRYPOINT ["/pr0sauce"]
