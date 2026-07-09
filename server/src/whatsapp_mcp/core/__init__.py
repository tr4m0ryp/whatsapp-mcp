"""Core layer: configuration, canonical models, and serialization."""

from .config import Config, load_config
from .models import Chat, Contact, Message, MessageContext
from .serialize import (
    chat_to_dict,
    contact_to_dict,
    format_message,
    format_messages_list,
    msg_to_dict,
)
from .transport import resolve_transport

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
    "Config",
    "load_config",
    "resolve_transport",
]
