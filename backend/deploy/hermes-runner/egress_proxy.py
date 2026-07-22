import asyncio
import ipaddress
import os
import socket
from collections.abc import Awaitable, Callable
from urllib.parse import urlsplit


MAX_HEADER_BYTES = 8_192
HEADER_TIMEOUT_SECONDS = 10.0
CONNECT_TIMEOUT_SECONDS = 15.0


class ProxyRejected(Exception):
    pass


Lookup = Callable[[str, int], Awaitable[list[tuple]]]
Connector = Callable[[int, str, int], Awaitable[tuple[asyncio.StreamReader, asyncio.StreamWriter]]]


def _parse_connect_line(raw: bytes) -> tuple[str, int]:
    try:
        method, authority, version = raw.decode("ascii").split(" ")
        parsed = urlsplit("//" + authority)
        port = parsed.port
    except (UnicodeDecodeError, ValueError) as exc:
        raise ProxyRejected("invalid_request") from exc
    if (
        method != "CONNECT"
        or version not in {"HTTP/1.0", "HTTP/1.1"}
        or not parsed.hostname
        or parsed.username is not None
        or parsed.password is not None
        or port is None
        or port < 1
        or port > 65_535
    ):
        raise ProxyRejected("invalid_target")
    return parsed.hostname, port


def _is_public_ip(raw: str) -> bool:
    try:
        address = ipaddress.ip_address(raw)
    except ValueError:
        return False
    return address.is_global and not (
        address.is_loopback
        or address.is_link_local
        or address.is_multicast
        or address.is_unspecified
        or address.is_reserved
    )


async def _default_lookup(host: str, port: int) -> list[tuple]:
    loop = asyncio.get_running_loop()
    return await loop.getaddrinfo(host, port, type=socket.SOCK_STREAM)


async def _resolve_public_targets(
    host: str, port: int, *, lookup: Lookup = _default_lookup
) -> list[tuple[int, str, int]]:
    try:
        resolved = await lookup(host, port)
    except OSError as exc:
        raise ProxyRejected("resolve_failed") from exc
    targets: list[tuple[int, str, int]] = []
    seen: set[tuple[int, str, int]] = set()
    for family, socktype, _proto, _canonname, sockaddr in resolved:
        if socktype != socket.SOCK_STREAM or family not in {socket.AF_INET, socket.AF_INET6}:
            continue
        address = str(sockaddr[0])
        if not _is_public_ip(address):
            # 混合返回集也整体拒绝，避免攻击者控制 DNS 轮转到内网地址。
            raise ProxyRejected("private_target")
        target = (family, address, port)
        if target not in seen:
            seen.add(target)
            targets.append(target)
    if not targets:
        raise ProxyRejected("resolve_failed")
    return targets


async def _default_connect(
    family: int, address: str, port: int
) -> tuple[asyncio.StreamReader, asyncio.StreamWriter]:
    return await asyncio.open_connection(address, port, family=family)


async def _connect_public_target(
    host: str,
    port: int,
    *,
    lookup: Lookup = _default_lookup,
    connector: Connector = _default_connect,
) -> tuple[asyncio.StreamReader, asyncio.StreamWriter]:
    targets = await _resolve_public_targets(host, port, lookup=lookup)
    last_error: Exception | None = None
    for family, address, resolved_port in targets:
        try:
            reader, writer = await asyncio.wait_for(
                connector(family, address, resolved_port),
                timeout=CONNECT_TIMEOUT_SECONDS,
            )
        except (OSError, TimeoutError) as exc:
            last_error = exc
            continue
        peer = writer.get_extra_info("peername")
        if not peer or not _is_public_ip(str(peer[0])):
            await _close_writer(writer)
            raise ProxyRejected("private_peer")
        return reader, writer
    raise ProxyRejected("connect_failed") from last_error


async def _copy_stream(reader: asyncio.StreamReader, writer: asyncio.StreamWriter) -> None:
    while chunk := await reader.read(65_536):
        writer.write(chunk)
        await writer.drain()
    try:
        writer.write_eof()
    except (AttributeError, OSError):
        pass


async def _close_writer(writer: asyncio.StreamWriter | None) -> None:
    if writer is None:
        return
    writer.close()
    try:
        await writer.wait_closed()
    except (ConnectionError, OSError):
        pass


async def _send_status(writer: asyncio.StreamWriter, status: bytes) -> None:
    writer.write(b"HTTP/1.1 " + status + b"\r\nConnection: close\r\n\r\n")
    await writer.drain()


async def handle_client(reader: asyncio.StreamReader, writer: asyncio.StreamWriter) -> None:
    upstream_writer: asyncio.StreamWriter | None = None
    try:
        header = await asyncio.wait_for(
            reader.readuntil(b"\r\n\r\n"), timeout=HEADER_TIMEOUT_SECONDS
        )
        if len(header) > MAX_HEADER_BYTES:
            raise ProxyRejected("headers_too_large")
        host, port = _parse_connect_line(header.split(b"\r\n", 1)[0])
        upstream_reader, upstream_writer = await _connect_public_target(host, port)
        await _send_status(writer, b"200 Connection Established")
        await asyncio.gather(
            _copy_stream(reader, upstream_writer),
            _copy_stream(upstream_reader, writer),
        )
    except (ProxyRejected, asyncio.IncompleteReadError, asyncio.LimitOverrunError, TimeoutError):
        try:
            await _send_status(writer, b"403 Forbidden")
        except (ConnectionError, OSError):
            pass
    except (ConnectionError, OSError):
        try:
            await _send_status(writer, b"502 Bad Gateway")
        except (ConnectionError, OSError):
            pass
    finally:
        await _close_writer(upstream_writer)
        await _close_writer(writer)


async def serve() -> None:
    bind = os.environ.get("HUAKAI_HERMES_EGRESS_BIND", "0.0.0.0:8080")
    host, separator, raw_port = bind.rpartition(":")
    if not separator or not host:
        raise SystemExit("HUAKAI_HERMES_EGRESS_BIND 必须是 host:port")
    try:
        port = int(raw_port)
    except ValueError as exc:
        raise SystemExit("HUAKAI_HERMES_EGRESS_BIND 端口必须是数字") from exc
    server = await asyncio.start_server(handle_client, host, port, limit=MAX_HEADER_BYTES)
    async with server:
        await server.serve_forever()


if __name__ == "__main__":
    asyncio.run(serve())
