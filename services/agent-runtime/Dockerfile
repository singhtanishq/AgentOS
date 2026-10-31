FROM python:3.13-slim AS base
WORKDIR /srv

COPY pyproject.toml ./
COPY app ./app

RUN pip install --no-cache-dir .

EXPOSE 8090
ENV AGENTOS_HTTP_ADDR=0.0.0.0
CMD ["python", "-m", "app"]
