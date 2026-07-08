"""Write-side access to the Go bridge's REST API."""

from .client import (
    download_media,
    send_audio_message,
    send_file,
    send_message,
    send_reaction,
)

__all__ = [
    "download_media",
    "send_audio_message",
    "send_file",
    "send_message",
    "send_reaction",
]
