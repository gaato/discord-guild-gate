FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /gate .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /gate /gate
EXPOSE 8081
ENTRYPOINT ["/gate"]
