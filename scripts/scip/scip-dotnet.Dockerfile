FROM mcr.microsoft.com/dotnet/sdk:10.0-noble@sha256:bef136a371963aaa46ec97882845ef6f54dec15728bb4b128b1c2a400ffbc9b4
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl unzip \
    && curl -fL https://api.nuget.org/v3-flatcontainer/scip-dotnet/0.2.14/scip-dotnet.0.2.14.nupkg -o /tmp/scip.nupkg \
    && echo 'e2d183fe39b9a56cb8bb2ed2d8b96828fb5434c6db084002bf8a5c6009391b52  /tmp/scip.nupkg' | sha256sum -c - \
    && unzip -q /tmp/scip.nupkg 'tools/net9.0/any/*' -d /tmp/tool \
    && mkdir /opt/scip && cp -a /tmp/tool/tools/net9.0/any/. /opt/scip/ \
    && rm -rf /tmp/scip.nupkg /tmp/tool && apt-get purge -y --auto-remove curl unzip && rm -rf /var/lib/apt/lists/*
