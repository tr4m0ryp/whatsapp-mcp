"""Read-side data access: SQLite queries over the bridge's databases."""

from .chats import get_chat, get_contact_chats, get_direct_chat_by_contact, list_chats
from .contacts import search_contacts
from .identity import get_sender_name, resolve_lid_to_phone, sender_aliases
from .messages import get_last_interaction, get_message_context, list_messages

__all__ = [
    "get_chat",
    "get_contact_chats",
    "get_direct_chat_by_contact",
    "get_last_interaction",
    "get_message_context",
    "get_sender_name",
    "list_chats",
    "list_messages",
    "resolve_lid_to_phone",
    "search_contacts",
    "sender_aliases",
]
