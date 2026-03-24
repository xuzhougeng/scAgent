"""Lightweight i18n module for the Python runtime.

Uses a shared ``locales/`` directory at the repository root.  Locale is stored
per-request in a :class:`threading.local` so that the ``ThreadingHTTPServer``
can serve different locales concurrently.

Usage::

    from i18n import t, set_request_locale

    set_request_locale("en")  # at the top of each request handler
    msg = t("runtime.filterCellsSummary", label="obj1", kept=100, removed=20)
"""

from __future__ import annotations

import json
import threading
from pathlib import Path
from typing import Any

_lock = threading.Lock()
_locales: dict[str, dict[str, str]] = {}
_current: threading.local = threading.local()

LOCALES_DIR = Path(__file__).resolve().parent.parent / "web" / "locales"
DEFAULT_LOCALE = "zh"


def load_locale(locale: str) -> dict[str, str]:
    with _lock:
        if locale not in _locales:
            path = LOCALES_DIR / f"{locale}.json"
            if path.exists():
                _locales[locale] = json.loads(path.read_text("utf-8"))
            else:
                _locales[locale] = {}
        return _locales[locale]


def set_request_locale(locale: str) -> None:
    _current.value = locale


def get_locale() -> str:
    return getattr(_current, "value", DEFAULT_LOCALE)


def t(key: str, **kwargs: Any) -> str:
    """Look up *key* in the current locale and interpolate *kwargs*.

    Falls back to the key itself when the translation is missing, so the
    system remains functional even if a key hasn't been translated yet.
    """
    locale_messages = load_locale(get_locale())
    template = locale_messages.get(key)
    if template is None:
        # Fallback: try default locale before giving up.
        if get_locale() != DEFAULT_LOCALE:
            fallback = load_locale(DEFAULT_LOCALE)
            template = fallback.get(key)
        if template is None:
            return key
    if kwargs:
        try:
            return template.format(**kwargs)
        except (KeyError, IndexError):
            return template
    return template
