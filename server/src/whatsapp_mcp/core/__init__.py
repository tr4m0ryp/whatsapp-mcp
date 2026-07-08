"""Core layer: configuration, canonical models, and serialization."""

from .models import Chat, Contact, Message, MessageContext
from .serialize import (
    chat_to_dict,
    contact_to_dict,
    format_message,
    format_messages_list,
    msg_to_dict,
)
from .transport import resolve_host, resolve_port, resolve_transport

__all__ = [
    "Chat",
    "Contact",
    "Message",
    "MessageContext",
    "chat_to_dict",
    "contact_to_dict",
    "format_message",
    "format_messages_list",
    "msg_to_dict",
    "resolve_host",
    "resolve_port",
    "resolve_transport",
]
