FROM alpine:3.22 AS build
RUN apk add --no-cache build-base linux-headers
COPY tests/writer-policy/check.c /src/check.c
RUN cc -std=c11 -Wall -Wextra -Werror -O2 -static /src/check.c -o /check
FROM alpine:3.22
COPY --from=build /check /check
ENTRYPOINT ["/check"]
