FROM mono:6.12
RUN printf "deb http://archive.debian.org/debian buster main\ndeb http://archive.debian.org/debian-security buster/updates main\ndeb http://archive.debian.org/debian buster-updates main\n" > /etc/apt/sources.list; \
    apt-get -o Acquire::Check-Valid-Until=false update && apt-get install -y --no-install-recommends python3 gcc libc6-dev git curl unzip ca-certificates procps && rm -rf /var/lib/apt/lists/*
COPY src /src
RUN git config --global --add safe.directory '*' && cd /src && tools/lab/build.sh HEAD /xg
WORKDIR /src
