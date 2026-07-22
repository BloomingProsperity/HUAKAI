FROM python:3.12-slim AS builder

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        gcc \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build
COPY requirements.txt .
RUN python -m venv /opt/venv \
    && /opt/venv/bin/pip install --no-cache-dir --require-hashes -r requirements.txt

FROM python:3.12-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PYTHONNOUSERSITE=1 \
    PATH=/opt/venv/bin:$PATH \
    HUAKAI_HERMES_WORK_ROOT=/run/huakai-hermes

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        tini \
    && rm -rf /var/lib/apt/lists/*

RUN groupadd --system --gid 10101 hermes \
    && useradd --system --uid 10101 --gid hermes --home-dir /home/hermes --create-home --shell /usr/sbin/nologin hermes

WORKDIR /app

COPY --from=builder /opt/venv /opt/venv
COPY requirements.txt .

COPY entrypoint.sh main.py jwt_verify.py official_runner.py official_tool_surface.py dependency_inventory.py egress_proxy.py sse_events.py ./
RUN python3 /app/dependency_inventory.py \
        --sbom /app/sbom.cdx.json \
        --licenses /app/python-licenses.json \
    && chmod +x /app/entrypoint.sh \
    && mkdir -p /run/huakai-hermes \
    && chown -R hermes:hermes /app /home/hermes /run/huakai-hermes

USER hermes
EXPOSE 8801

HEALTHCHECK --interval=10s --timeout=3s --retries=5 \
    CMD ["python3", "-c", "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8801/healthz', timeout=2).read()"]

ENTRYPOINT ["/usr/bin/tini", "--", "/app/entrypoint.sh"]
