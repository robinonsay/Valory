FROM golang:1.23-bookworm

RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
    && apt-get install -y --no-install-recommends \
        nodejs \
        openssl \
        postgresql-client \
        ruby \
        ruby-dev \
    && gem install asciidoctor asciidoctor-pdf --no-document \
    && rm -rf /var/lib/apt/lists/*

RUN useradd -m -s /bin/bash valory

USER valory
WORKDIR /home/valory
