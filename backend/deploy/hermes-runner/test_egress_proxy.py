import asyncio
import socket
import unittest

import egress_proxy


class EgressProxyTests(unittest.TestCase):
    def test_connect只接受无凭据的合法目标(self):
        self.assertEqual(
            egress_proxy._parse_connect_line(b"CONNECT api.example.com:443 HTTP/1.1"),
            ("api.example.com", 443),
        )
        for raw in (
            b"GET api.example.com:443 HTTP/1.1",
            b"CONNECT user:pass@api.example.com:443 HTTP/1.1",
            b"CONNECT api.example.com HTTP/1.1",
            b"CONNECT api.example.com:0 HTTP/1.1",
        ):
            with self.subTest(raw=raw), self.assertRaises(egress_proxy.ProxyRejected):
                egress_proxy._parse_connect_line(raw)

    def test公网判定拒绝内网和特殊地址(self):
        for raw in (
            "127.0.0.1",
            "10.0.0.1",
            "169.254.169.254",
            "100.64.0.1",
            "::1",
            "fc00::1",
        ):
            with self.subTest(raw=raw):
                self.assertFalse(egress_proxy._is_public_ip(raw))
        self.assertTrue(egress_proxy._is_public_ip("8.8.8.8"))
        self.assertTrue(egress_proxy._is_public_ip("2606:4700:4700::1111"))

    def test域名混合解析结果整体拒绝(self):
        async def lookup(_host, port):
            return [
                (socket.AF_INET, socket.SOCK_STREAM, 6, "", ("8.8.8.8", port)),
                (socket.AF_INET, socket.SOCK_STREAM, 6, "", ("127.0.0.1", port)),
            ]

        with self.assertRaises(egress_proxy.ProxyRejected):
            asyncio.run(
                egress_proxy._resolve_public_targets("mixed.example", 443, lookup=lookup)
            )

    def test拨号使用已校验IP而不是重新解析域名(self):
        captured = []

        async def lookup(_host, port):
            return [(socket.AF_INET, socket.SOCK_STREAM, 6, "", ("8.8.8.8", port))]

        class Writer:
            def get_extra_info(self, name):
                return ("8.8.8.8", 443) if name == "peername" else None

        async def connector(family, address, port):
            captured.append((family, address, port))
            return asyncio.StreamReader(), Writer()

        asyncio.run(
            egress_proxy._connect_public_target(
                "rebind.example", 443, lookup=lookup, connector=connector
            )
        )
        self.assertEqual(captured, [(socket.AF_INET, "8.8.8.8", 443)])

    def test连接已被对端重置时关闭操作保持幂等(self):
        class ResetWriter:
            def __init__(self):
                self.closed = False

            def close(self):
                self.closed = True

            async def wait_closed(self):
                raise ConnectionResetError("peer reset")

        writer = ResetWriter()
        asyncio.run(egress_proxy._close_writer(writer))
        self.assertTrue(writer.closed)


if __name__ == "__main__":
    unittest.main()
