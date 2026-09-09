FROM node:22-bookworm-slim

# Install system utilities and build tools
RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    curl \
    ca-certificates \
    procps \
    python3 \
    python3-pip \
    python3-venv \
    build-essential \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /workspace

# Install OpenCode binary
ENV PATH="/root/.opencode/bin:${PATH}"
RUN curl -fsSL https://opencode.ai/install | bash

# Configure Git default identity for commit testing inside container
RUN git config --global user.name "Albert Code Agent" && \
    git config --global user.email "albert-code@noreply.etalab.gouv.fr" && \
    git config --global --add safe.directory '*'

EXPOSE 4096

ENTRYPOINT ["opencode"]
CMD ["serve", "--hostname", "0.0.0.0", "--port", "4096"]
