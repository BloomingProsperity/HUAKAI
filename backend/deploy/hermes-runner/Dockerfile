FROM python:3.12-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        gcc \
        tini \
    && rm -rf /var/lib/apt/lists/*

RUN groupadd --system --gid 10101 hermes \
    && useradd --system --uid 10101 --gid hermes --home-dir /app --shell /usr/sbin/nologin hermes

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY entrypoint.sh main.py ./
RUN chmod +x /app/entrypoint.sh \
    && mkdir -p /var/lib/huakai/hermes \
    && chown -R hermes:hermes /app /var/lib/huakai/hermes

USER hermes
EXPOSE 8801

HEALTHCHECK --interval=10s --timeout=3s --retries=5 \
    CMD curl -fsS http://127.0.0.1:8801/healthz >/dev/null || exit 1

ENTRYPOINT ["/usr/bin/tini", "--", "/app/entrypoint.sh"]
