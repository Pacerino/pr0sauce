import os

import httpx
import pytest
import respx
from fastapi.testclient import TestClient

from app import main

URL = "https://vid.pr0gramm.com/2025/10/22/abc-h264-full_hd.mp4"
RESULT = {"matches": [{"id": "1"}], "track": {"title": "Kumite", "subtitle": "Paul Hertzog"}}


@pytest.fixture
def client():
    return TestClient(main.app)


def fake_recognizer(seen, result=RESULT, exc=None):
    class FakeShazam:
        async def recognize(self, path):
            seen.append(path)
            assert os.path.exists(path)
            if exc:
                raise exc
            return result

    return FakeShazam


def test_healthz(client):
    r = client.get("/healthz")
    assert r.status_code == 200
    assert r.json() == {"ok": True}


@respx.mock
def test_recognize_returns_raw(client, monkeypatch):
    seen = []
    monkeypatch.setattr(main, "Shazam", fake_recognizer(seen))
    respx.get(URL).mock(return_value=httpx.Response(200, content=b"video-bytes"))
    r = client.post("/recognize", json={"url": URL})
    assert r.status_code == 200
    assert r.json() == RESULT
    assert len(seen) == 1 and not os.path.exists(seen[0])


@pytest.mark.parametrize(
    "url",
    [
        "https://evil.example.com/a.mp4",
        "http://vid.pr0gramm.com/a.mp4",
        "https://vid.pr0gramm.com.evil.com/a.mp4",
        "https://user@evil.com/@vid.pr0gramm.com/a.mp4",
        "not a url",
    ],
)
def test_rejects_foreign_host(client, url):
    assert client.post("/recognize", json={"url": url}).status_code == 422


@respx.mock
def test_download_404(client):
    respx.get(URL).mock(return_value=httpx.Response(404))
    r = client.post("/recognize", json={"url": URL})
    assert r.status_code == 404
    assert r.json() == {"error": "not_found"}


@respx.mock
def test_download_500_is_502(client):
    respx.get(URL).mock(return_value=httpx.Response(500))
    r = client.post("/recognize", json={"url": URL})
    assert r.status_code == 502
    assert "error" in r.json()


@respx.mock
def test_temp_file_removed_on_error(client, monkeypatch):
    seen = []
    monkeypatch.setattr(main, "Shazam", fake_recognizer(seen, exc=RuntimeError("boom")))
    respx.get(URL).mock(return_value=httpx.Response(200, content=b"video-bytes"))
    r = client.post("/recognize", json={"url": URL})
    assert r.status_code == 502
    assert r.json() == {"error": "boom"}
    assert len(seen) == 1 and not os.path.exists(seen[0])


@respx.mock
def test_too_large_aborts(client, monkeypatch):
    monkeypatch.setattr(main, "MAX_BYTES", 10)
    created = []
    real = main.tempfile.NamedTemporaryFile

    def spy(*a, **kw):
        f = real(*a, **kw)
        created.append(f.name)
        return f

    monkeypatch.setattr(main.tempfile, "NamedTemporaryFile", spy)
    respx.get(URL).mock(return_value=httpx.Response(200, content=b"x" * 100))
    r = client.post("/recognize", json={"url": URL})
    assert r.status_code == 502
    assert r.json() == {"error": "too_large"}
    assert created and not os.path.exists(created[0])
