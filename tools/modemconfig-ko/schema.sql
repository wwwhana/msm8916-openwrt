-- Storage design informed by TTAK.KO-06.0410/R9, not a standard-defined SQL schema.
-- §1 delegates SMS/MMS wire requirements to each operator. Their metadata is
-- separate from the shared conversation and message model.
PRAGMA journal_mode=WAL;
PRAGMA synchronous=FULL;
PRAGMA foreign_keys=ON;
BEGIN IMMEDIATE;

CREATE TABLE IF NOT EXISTS subscriptions(
    id TEXT PRIMARY KEY, fingerprint TEXT NOT NULL UNIQUE,
    slot INTEGER NOT NULL CHECK(slot BETWEEN 1 AND 5), provider TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS contacts(
    id TEXT PRIMARY KEY, name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 80),
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS contact_addresses(
    contact_id TEXT NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
    address TEXT NOT NULL UNIQUE, label TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL DEFAULT 'tel' CHECK(kind IN('tel','sip','email')),
    PRIMARY KEY(contact_id,address)
);

-- §§7.3, 8.6, 8.8, 8.9: network identity survives rejoin and participant exit;
-- local title/icon overrides must not overwrite the shared network values.
CREATE TABLE IF NOT EXISTS conversations(
    id TEXT PRIMARY KEY,
    subscription_id TEXT NOT NULL REFERENCES subscriptions(id),
    kind TEXT NOT NULL CHECK(kind IN('direct','group','chatbot')),
    peer TEXT, conversation_id TEXT, contribution_id TEXT, focus_uri TEXT,
    subject TEXT, local_subject TEXT,
    icon_part_id TEXT REFERENCES message_parts(id) ON DELETE SET NULL,
    local_icon_part_id TEXT REFERENCES message_parts(id) ON DELETE SET NULL,
    created_by TEXT, invited_by TEXT, created_at TEXT NOT NULL,
    UNIQUE(subscription_id,kind,peer),
    CHECK(kind!='group' OR conversation_id IS NULL OR contribution_id IS NULL
          OR conversation_id=contribution_id)
);
CREATE TABLE IF NOT EXISTS participants(
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    address TEXT NOT NULL, alias TEXT, role TEXT,
    state TEXT NOT NULL DEFAULT 'active', joined_at TEXT, left_at TEXT,
    PRIMARY KEY(conversation_id,address)
);

-- A semantic message is not a transport attempt: §§7.2/7.4 allow fallback.
-- Local read time is separate from a peer's IMDN displayed notification.
CREATE TABLE IF NOT EXISTS messages(
    seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    source_key TEXT NOT NULL, protocol TEXT NOT NULL CHECK(protocol IN('sms','mms','rcs')),
    direction TEXT NOT NULL CHECK(direction IN('incoming','outgoing')),
    sender TEXT NOT NULL, body TEXT NOT NULL DEFAULT '', subject TEXT,
    sent_at TEXT, received_at TEXT, created_at TEXT NOT NULL,
    local_read_at TEXT, network_message_id TEXT, cpim_message_id TEXT,
    reference_id TEXT, reference_type INTEGER, reference_value TEXT,
    canceled_at TEXT, edited_at TEXT, edit_count INTEGER NOT NULL DEFAULT 0,
    traffic_type TEXT, advised_action TEXT, warning_source TEXT,
    UNIQUE(conversation_id,source_key)
);
CREATE INDEX IF NOT EXISTS messages_thread ON messages(conversation_id,seq DESC);
CREATE TABLE IF NOT EXISTS message_recipients(
    message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    address TEXT NOT NULL, role TEXT NOT NULL CHECK(role IN('to','cc','bcc')),
    PRIMARY KEY(message_id,address,role)
);
CREATE TABLE IF NOT EXISTS transport_attempts(
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    protocol TEXT NOT NULL CHECK(protocol IN('sms','mms','rcs')),
    request_id TEXT, request_digest TEXT, session_id TEXT, state TEXT NOT NULL,
    attempt_number INTEGER NOT NULL DEFAULT 1, fallback_from INTEGER REFERENCES transport_attempts(id),
    created_at TEXT NOT NULL, updated_at TEXT NOT NULL, submitted_at TEXT,
    failure_domain TEXT, failure_code INTEGER, failure_reason TEXT,
    UNIQUE(message_id,attempt_number)
);
CREATE INDEX IF NOT EXISTS queued_transports ON transport_attempts(state,id);
CREATE UNIQUE INDEX IF NOT EXISTS attempt_request ON transport_attempts(message_id,request_id);
-- A multipart SMS is complete only after all segments reach that state.
CREATE TABLE IF NOT EXISTS sms_submission_segments(
    attempt_id INTEGER NOT NULL REFERENCES transport_attempts(id) ON DELETE CASCADE,
    part_number INTEGER NOT NULL, part_count INTEGER NOT NULL,
    call_id TEXT, rp_reference INTEGER, tp_reference INTEGER,
    state TEXT NOT NULL, updated_at TEXT NOT NULL,
    PRIMARY KEY(attempt_id,part_number)
);

-- §§7.2.4, 7.3 and 8.4: delivered/displayed/canceled are recipient events;
-- one server's acceptance never implies delivery to every participant.
CREATE TABLE IF NOT EXISTS delivery_events(
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    attempt_id INTEGER REFERENCES transport_attempts(id) ON DELETE CASCADE,
    recipient TEXT NOT NULL, kind TEXT NOT NULL,
    remote_event_id TEXT, occurred_at TEXT NOT NULL, code INTEGER,
    UNIQUE(message_id,recipient,kind,remote_event_id)
);
CREATE TABLE IF NOT EXISTS sms_metadata(
    message_id TEXT PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
    smsc TEXT, tp_reference INTEGER, protocol_identifier INTEGER, data_coding_scheme INTEGER,
    concatenation_reference INTEGER, part_count INTEGER, status_report_requested INTEGER,
    service_center_time TEXT, validity_period TEXT, message_class INTEGER, reply_path INTEGER
);
-- Commit each original RP-DATA segment before positively acknowledging it.
CREATE TABLE IF NOT EXISTS sms_segments(
    id TEXT PRIMARY KEY,
    subscription_id TEXT NOT NULL REFERENCES subscriptions(id),
    message_id TEXT REFERENCES messages(id) ON DELETE CASCADE,
    call_id TEXT NOT NULL, sender TEXT, recipient TEXT,
    concat_reference INTEGER, concat_bits INTEGER, part_number INTEGER, part_count INTEGER,
    destination_port INTEGER, source_port INTEGER,
    tpdu BLOB NOT NULL, metadata_json TEXT NOT NULL, received_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS mms_metadata(
    message_id TEXT PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
    transaction_id TEXT, mms_message_id TEXT, mms_version TEXT, message_type TEXT,
    content_location TEXT, expires_at TEXT, message_size INTEGER,
    message_class TEXT, priority TEXT, response_status INTEGER, retrieve_status INTEGER,
    delivery_report_requested INTEGER, read_report_requested INTEGER
);
-- Retain notification/retrieval/report PDUs independently of the decoded parts.
-- Large original PDUs may live in a private file; the digest verifies that file.
CREATE TABLE IF NOT EXISTS mms_pdus(
    id TEXT PRIMARY KEY,
    subscription_id TEXT NOT NULL REFERENCES subscriptions(id),
    message_id TEXT REFERENCES messages(id) ON DELETE CASCADE,
    transaction_id TEXT, message_type TEXT NOT NULL,
    direction TEXT NOT NULL CHECK(direction IN('incoming','outgoing')),
    payload BLOB, local_path TEXT, byte_size INTEGER, sha256 TEXT,
    occurred_at TEXT NOT NULL,
    CHECK(payload IS NOT NULL OR local_path IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS mms_transaction ON mms_pdus(subscription_id,transaction_id);
-- §7.4: MIME parts, thumbnail links, transfer progress and expiring remote URLs.
-- MMS content-id/SMIL metadata stays distinct from RCS HTTP transfer metadata.
CREATE TABLE IF NOT EXISTS message_parts(
    id TEXT PRIMARY KEY,
    message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    position INTEGER NOT NULL, mime_type TEXT NOT NULL, charset TEXT,
    filename TEXT, content_id TEXT, content_location TEXT, disposition TEXT,
    text_content TEXT, local_path TEXT, byte_size INTEGER, sha256 TEXT,
    remote_url TEXT, remote_expires_at TEXT, transfer_id TEXT,
    transfer_state TEXT, transferred_bytes INTEGER NOT NULL DEFAULT 0,
    thumbnail_part_id TEXT REFERENCES message_parts(id),
    duration_ms INTEGER, width INTEGER, height INTEGER,
    UNIQUE(message_id,position)
);
-- §§8.2/8.3/8.10 retain the remote target ID even if the target is not stored yet.
CREATE TABLE IF NOT EXISTS message_changes(
    id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    target_message_id TEXT, actor TEXT NOT NULL,
    kind TEXT NOT NULL CHECK(kind IN('reply','reaction','cancel','edit')),
    value TEXT, active INTEGER, occurred_at TEXT NOT NULL,
    source_message_id TEXT REFERENCES messages(id) ON DELETE SET NULL
);
CREATE TABLE IF NOT EXISTS capabilities(
    subscription_id TEXT NOT NULL REFERENCES subscriptions(id), address TEXT NOT NULL,
    features TEXT NOT NULL, checked_at TEXT NOT NULL, expires_at TEXT NOT NULL,
    PRIMARY KEY(subscription_id,address)
);
CREATE TABLE IF NOT EXISTS drafts(
    conversation_id TEXT PRIMARY KEY REFERENCES conversations(id) ON DELETE CASCADE,
    body TEXT NOT NULL, requested_protocol TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS migrations(source TEXT PRIMARY KEY, completed_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS message_uploads(
    id TEXT PRIMARY KEY, subscription_id TEXT NOT NULL REFERENCES subscriptions(id),
    session_id TEXT NOT NULL, filename TEXT NOT NULL, mime_type TEXT NOT NULL,
    byte_size INTEGER NOT NULL, received_bytes INTEGER NOT NULL DEFAULT 0,
    sha256 TEXT, created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS nas_status_reports(
    id TEXT PRIMARY KEY, subscription_id TEXT NOT NULL REFERENCES subscriptions(id),
    recipient TEXT NOT NULL, tp_reference INTEGER NOT NULL, status INTEGER NOT NULL,
    service_center_time TEXT NOT NULL, received_at TEXT NOT NULL, tpdu BLOB NOT NULL,
    processed INTEGER NOT NULL DEFAULT 0, attempt_id INTEGER REFERENCES transport_attempts(id) ON DELETE SET NULL
);
-- Delivery/read receipts survive a lost chat session and a daemon restart.
CREATE TABLE IF NOT EXISTS rcs_receipts(
    message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK(kind IN('delivered','displayed')),
    receipt_id TEXT NOT NULL UNIQUE, requested_at TEXT NOT NULL, sent_at TEXT,
    attempts INTEGER NOT NULL DEFAULT 0, next_attempt_at TEXT NOT NULL,
    PRIMARY KEY(message_id,kind)
);
CREATE TABLE IF NOT EXISTS calls(
    seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE,
    subscription_id TEXT NOT NULL REFERENCES subscriptions(id), peer TEXT NOT NULL,
    direction TEXT NOT NULL CHECK(direction IN('incoming','outgoing')),
    started_at TEXT NOT NULL, connected_at TEXT, ended_at TEXT,
    duration_seconds INTEGER NOT NULL DEFAULT 0 CHECK(duration_seconds>=0),
    audio TEXT NOT NULL, result TEXT CHECK(result IN('completed','missed','rejected','canceled','failed','interrupted')),
    failure TEXT
);
CREATE INDEX IF NOT EXISTS calls_started ON calls(started_at DESC,seq DESC);
PRAGMA user_version=5;
COMMIT;
