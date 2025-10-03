FROM postgres:17-alpine

# Install build dependencies for pg_uuidv7 extension
RUN apk add --no-cache \
        build-base postgresql-dev git clang llvm20 && \
    rm -rf /var/cache/apk/*

# Clone and build pg_uuidv7 extension
RUN cd /tmp && \
    git clone https://github.com/fboulnois/pg_uuidv7.git && \
    cd pg_uuidv7 && \
    ln -sf /usr/bin/clang /usr/bin/clang-19 && \
    mkdir -p /usr/lib/llvm19/bin && \
    ln -sf /usr/bin/llvm-lto /usr/lib/llvm19/bin/llvm-lto && \
    make && \
    make install && \
    cd / && \
    rm -rf /tmp/pg_uuidv7

# Clean up build dependencies
RUN apk del build-base postgresql-dev git clang llvm20
