"""Shazam sidecar: download a pr0gramm video by URL and return shazamio's raw result."""
import asyncio
import os
import tempfile
from urllib.parse import urlsplit

import httpx
from fastapi import FastAPI, HTTPException
from fastapi.responses import JSONResponse
from pydantic import BaseModel
from shazamio import Shazam

ALLOWED_HOST = "vid.pr0gramm.com"
MAX_BYTES = 200 * 1024 * 1024
DOWNLOAD_TIMEOUT = 60.0

app = FastAPI()


class RecognizeRequest(BaseModel):
    url: str


class NotFound(Exception):
    pass


class TooLarge(Exception):
    pass


def check_url(url: str) -> None:
    try:
        parts = urlsplit(url)
        host = parts.hostname
        # .port raises ValueError on a malformed port
        parts.port
    except ValueError:
        raise HTTPException(status_code=422, detail="invalid url")
    if parts.scheme != "https" or host != ALLOWED_HOST or parts.username or parts.password:
        raise HTTPException(status_code=422, detail="only https://vid.pr0gramm.com urls are allowed")


async def download(url: str, dest) -> None:
    async with asyncio.timeout(DOWNLOAD_TIMEOUT):
        # no redirects: a redirect could leave the allowlisted host
        async with httpx.AsyncClient(follow_redirects=False) as client:
            async with client.stream("GET", url) as resp:
                if resp.status_code == 404:
                    raise NotFound()
                resp.raise_for_status()
                size = 0
                async for chunk in resp.aiter_bytes():
                    size += len(chunk)
                    if size > MAX_BYTES:
                        raise TooLarge()
                    dest.write(chunk)


@app.get("/healthz")
async def healthz():
    return {"ok": True}


@app.post("/recognize")
async def recognize(req: RecognizeRequest):
    check_url(req.url)
    f = tempfile.NamedTemporaryFile(suffix=".mp4", delete=False)
    path = f.name
    try:
        try:
            with f:
                await download(req.url, f)
            return await Shazam().recognize(path)
        except NotFound:
            return JSONResponse({"error": "not_found"}, status_code=404)
        except TooLarge:
            return JSONResponse({"error": "too_large"}, status_code=502)
        except TimeoutError:
            return JSONResponse({"error": "download_timeout"}, status_code=502)
        except Exception as e:  # download or recognition failure
            return JSONResponse({"error": str(e) or type(e).__name__}, status_code=502)
    finally:
        try:
            os.unlink(path)
        except FileNotFoundError:
            pass
