// reminders-eventkit-bridge
//
// M13 Slice 0 proved a standalone macOS process can read real Apple
// Reminders through EventKit. M13 Slice 3A evolves this same binary into a
// real Lum Harbor Plugin artifact: with no arguments (exactly how the Plugin
// Host launches it — internal/plugin/process.go calls
// exec.CommandContext(ctx, extension.ExecutablePath) with no args), it runs
// the Lum Harbor Plugin Runtime V1 protocol directly over stdio — no Go
// wrapper subprocess. It still never creates, edits, completes or deletes a
// Reminder: it declares and serves only source.fetch.
//
// Usage:
//   reminders-eventkit-bridge            # serve the Plugin protocol on stdio (Host entry point)
//   reminders-eventkit-bridge status     # debug: print current authorization status, exit
//   reminders-eventkit-bridge request    # debug: request full access, print outcome, exit
//   reminders-eventkit-bridge list       # debug: request access if needed, dump raw EventKit
//                                         # evidence for every reminder as one JSON object
//                                         # (distinct from, and never used by, the protocol path)

import EventKit
import Foundation

// MARK: - Plugin identity (must match plugin.json exactly; system.handshake
// is compared field-by-field against it, both directions)

let extensionID = "lh-plugin-apple-reminders"
let extensionVersion = "0.1.0"
let protocolVersion = 1
let sourceFetchCapability = "source.fetch"
let sourceKindReminder = "REMINDER"

// MARK: - Debug evidence model (used only by the "list" debug command, never
// by the protocol-serving path below)

/// One field-for-field capture of what EventKit actually returns for a
/// Reminder, not a Lum Harbor domain shape. Deliberately verbose: this is
/// evidence for manual inspection, not the wire payload.
struct ReminderRecord: Codable {
    let calendarItemIdentifier: String
    let calendarItemExternalIdentifier: String?
    let calendarTitle: String
    let title: String
    let notes: String?
    let isCompleted: Bool
    let completionDateISO8601: String?
    let creationDateISO8601: String?
    let lastModifiedDateISO8601: String?
    let timeZoneIdentifier: String?

    // Raw due-date components exactly as EventKit reports them, so date-only
    // vs timed vs absent can be inspected without guessing.
    let dueYear: Int?
    let dueMonth: Int?
    let dueDay: Int?
    let dueHour: Int?
    let dueMinute: Int?
    let dueSecond: Int?
    let dueDateComponentsDebugDescription: String?

    /// Derived classification for readability in evidence output only.
    /// NONE = no dueDateComponents at all.
    /// DATE_ONLY = year/month/day present, hour/minute absent.
    /// TIMED = hour and/or minute present.
    let dueKind: String
}

struct ListResult: Codable {
    let generatedAtISO8601: String
    let authorizationStatus: String
    let reminderCount: Int
    let reminders: [ReminderRecord]
}

// MARK: - Shared helpers (used by both the debug commands and the protocol path)

let iso8601 = ISO8601DateFormatter()
iso8601.formatOptions = [.withInternetDateTime, .withFractionalSeconds]

func isoString(_ date: Date?) -> String? {
    guard let date else { return nil }
    return iso8601.string(from: date)
}

func classifyDueKind(_ components: DateComponents?) -> String {
    guard let components else { return "NONE" }
    if components.hour != nil || components.minute != nil {
        return "TIMED"
    }
    if components.year != nil || components.month != nil || components.day != nil {
        return "DATE_ONLY"
    }
    return "NONE"
}

func authorizationStatusName(_ status: EKAuthorizationStatus) -> String {
    switch status {
    case .notDetermined: return "notDetermined"
    case .restricted: return "restricted"
    case .denied: return "denied"
    case .authorized: return "authorized" // legacy pre-macOS 14 granted value
    case .fullAccess: return "fullAccess"
    case .writeOnly: return "writeOnly" // not expected for .reminder, present for completeness
    @unknown default: return "unknown(\(status.rawValue))"
    }
}

func fail(_ message: String) -> Never {
    FileHandle.standardError.write((message + "\n").data(using: .utf8)!)
    exit(1)
}

// MARK: - EventKit access (the only code that ever touches EventKit; both
// the debug "list" command and the protocol's source.fetch handler call
// exactly these two functions — there is no second, divergent EventKit
// reader)

let store = EKEventStore()

/// Requests full Reminders access using the modern per-OS-version supported
/// entry point, and reports exactly what happened (granted, denied, or
/// error) rather than assuming success. Slice 0 found the completion result
/// can report granted=false transiently even though authorizationStatus
/// becomes authorized/fullAccess moments later — callers must re-check
/// EKEventStore.authorizationStatus(for:) themselves rather than trusting
/// this return value alone.
@MainActor
func requestFullAccess() async -> (granted: Bool, errorDescription: String?) {
    if #available(macOS 14.0, *) {
        do {
            let granted = try await store.requestFullAccessToReminders()
            return (granted, nil)
        } catch {
            return (false, String(describing: error))
        }
    } else {
        return await withCheckedContinuation { continuation in
            store.requestAccess(to: .reminder) { granted, error in
                continuation.resume(returning: (granted, error.map { String(describing: $0) }))
            }
        }
    }
}

@MainActor
func fetchAllReminders() async -> [EKReminder] {
    let calendars = store.calendars(for: .reminder)
    let predicate = store.predicateForReminders(in: calendars.isEmpty ? nil : calendars)
    return await withCheckedContinuation { continuation in
        store.fetchReminders(matching: predicate) { reminders in
            continuation.resume(returning: reminders ?? [])
        }
    }
}

func toRecord(_ reminder: EKReminder) -> ReminderRecord {
    let due = reminder.dueDateComponents
    return ReminderRecord(
        calendarItemIdentifier: reminder.calendarItemIdentifier,
        calendarItemExternalIdentifier: reminder.calendarItemExternalIdentifier,
        calendarTitle: reminder.calendar?.title ?? "",
        title: reminder.title ?? "",
        notes: reminder.hasNotes ? reminder.notes : nil,
        isCompleted: reminder.isCompleted,
        completionDateISO8601: isoString(reminder.completionDate),
        creationDateISO8601: isoString(reminder.creationDate),
        lastModifiedDateISO8601: isoString(reminder.lastModifiedDate),
        timeZoneIdentifier: reminder.timeZone?.identifier,
        dueYear: due?.year,
        dueMonth: due?.month,
        dueDay: due?.day,
        dueHour: due?.hour,
        dueMinute: due?.minute,
        dueSecond: due?.second,
        dueDateComponentsDebugDescription: due.map { String(describing: $0) },
        dueKind: classifyDueKind(due)
    )
}

// MARK: - Lum Harbor Plugin Runtime V1 protocol
//
// Mirrors lh-plugin-base/main.go's shape exactly (newline-JSON stdio,
// synchronous read-handle-write loop, one process for the lifetime of the
// extension) using Foundation's JSONSerialization instead of Codable, so
// optional/omitted JSON keys are controlled explicitly (Swift's Codable
// would otherwise emit `null` for a nil Optional; Go's decoder rejects
// unexpected top-level envelope keys — see internal/plugin/protocol.go's
// decodeRPCResponse — but does not require every field to be present, so
// this file only ever adds a key when it has a real value).

func logStderr(_ message: String) {
    FileHandle.standardError.write("\(extensionID): \(message)\n".data(using: .utf8)!)
}

func writeJSONLine(_ object: [String: Any]) {
    guard let data = try? JSONSerialization.data(withJSONObject: object, options: []) else {
        logStderr("failed to encode response for \(object)")
        return
    }
    FileHandle.standardOutput.write(data)
    FileHandle.standardOutput.write(Data([0x0A]))
}

func successResponse(id: String, result: [String: Any]) -> [String: Any] {
    ["id": id, "result": result]
}

func errorResponse(id: String, code: String, message: String) -> [String: Any] {
    ["id": id, "error": ["code": code, "message": message]]
}

/// decodeRequestLine parses one already-read line into a request object, or
/// nil if the line is not a well-formed JSON object. This is deliberately
/// separate from reading the line itself: a malformed line must only skip
/// that one request (matching lh-plugin-base's "ignored invalid request:
/// continue"), never be confused with true stdin EOF.
func decodeRequestLine(_ line: String) -> [String: Any]? {
    guard let data = line.data(using: .utf8) else { return nil }
    return (try? JSONSerialization.jsonObject(with: data, options: [])) as? [String: Any]
}

func handleHandshake(id: String, params: [String: Any]) {
    guard
        let hostProtocolVersion = params["protocolVersion"] as? Int,
        let hostExtensionID = params["extensionId"] as? String,
        let hostExtensionVersion = params["extensionVersion"] as? String,
        let hostCapabilities = params["capabilities"] as? [String]
    else {
        writeJSONLine(errorResponse(id: id, code: "invalid_handshake", message: "malformed handshake params"))
        return
    }
    let matches = hostProtocolVersion == protocolVersion
        && hostExtensionID == extensionID
        && hostExtensionVersion == extensionVersion
        && Set(hostCapabilities) == Set([sourceFetchCapability])
    guard matches else {
        writeJSONLine(errorResponse(id: id, code: "incompatible_handshake", message: "host handshake does not match this plugin artifact"))
        return
    }
    writeJSONLine(successResponse(id: id, result: [
        "protocolVersion": protocolVersion,
        "extensionId": extensionID,
        "extensionVersion": extensionVersion,
        "capabilities": [sourceFetchCapability],
    ]))
}

/// classifyAndBuildDue turns raw EventKit due-date components into the
/// closed (kind, year, month, day, hour, minute, second) shape M13 Slice 2B's
/// reminderPayloadDTO requires. It never crashes and never invents a real
/// date: a genuinely unusable/degenerate component set (which normal
/// Reminders usage cannot produce) falls back to NONE.
///
/// EventKit's due-time granularity is minutes: a reminder timed through the
/// Reminders app UI carries hour/minute but not always an explicit `second`
/// component. Treating a missing `second` (and, defensively, a missing
/// `minute`) as :00 is filling in an always-implied zero, not fabricating
/// data — the same as how a clock display omits ":00" seconds.
func classifyAndBuildDue(_ due: DateComponents?) -> (kind: String, year: Int?, month: Int?, day: Int?, hour: Int, minute: Int, second: Int) {
    guard let due else { return ("NONE", nil, nil, nil, 0, 0, 0) }
    let hasDate = due.year != nil && due.month != nil && due.day != nil
    let hasTime = due.hour != nil || due.minute != nil
    if hasDate && hasTime {
        return ("TIMED", due.year, due.month, due.day, due.hour ?? 0, due.minute ?? 0, due.second ?? 0)
    }
    if hasDate {
        return ("DATE_ONLY", due.year, due.month, due.day, 0, 0, 0)
    }
    return ("NONE", nil, nil, nil, 0, 0, 0)
}

/// reminderToPayload maps one EKReminder into exactly the structured JSON
/// contract internal/plannersync/payload.go's reminderPayloadDTO decodes
/// (title/notes/completed/completedAt/dueKind + due components/timeZone).
/// It never includes a second identity field: stable identity is carried
/// only by the enclosing SourceRecord's externalId (calendarItemIdentifier),
/// never duplicated here.
func reminderToPayload(_ reminder: EKReminder) -> [String: Any] {
    var payload: [String: Any] = [
        "title": reminder.title ?? "",
        "completed": reminder.isCompleted,
    ]
    if reminder.hasNotes, let notes = reminder.notes {
        payload["notes"] = notes
    }
    if reminder.isCompleted, let completionDate = reminder.completionDate {
        payload["completedAt"] = isoString(completionDate)
    }
    // If isCompleted but EventKit gave no completionDate (not expected in
    // practice), completedAt is left absent rather than invented — Slice 2B
    // already treats that as a malformed record and fails it in isolation.

    let due = classifyAndBuildDue(reminder.dueDateComponents)
    payload["dueKind"] = due.kind
    switch due.kind {
    case "DATE_ONLY":
        payload["dueYear"] = due.year!
        payload["dueMonth"] = due.month!
        payload["dueDay"] = due.day!
    case "TIMED":
        payload["dueYear"] = due.year!
        payload["dueMonth"] = due.month!
        payload["dueDay"] = due.day!
        payload["dueHour"] = due.hour
        payload["dueMinute"] = due.minute
        payload["dueSecond"] = due.second
        if let timeZoneID = reminder.timeZone?.identifier {
            payload["timeZone"] = timeZoneID
        }
        // A TIMED reminder with no EventKit timeZone is not expected in
        // practice (Slice 0 observed one is always present for app-created
        // timed reminders); timeZone is left absent rather than guessed —
        // Slice 2B rejects a TIMED record with no timezone as malformed and
        // fails only that record.
    default:
        if reminder.dueDateComponents != nil {
            logStderr("reminder \(reminder.calendarItemIdentifier) has unusable dueDateComponents; treating as NONE")
        }
    }
    return payload
}

/// reminderToSourceRecord wraps reminderToPayload with the outer
/// internal/plugin.SourceRecord envelope (externalId/sourceKind/observedAt/
/// sourceUpdatedAt/payload). ExternalID is always EventKit's stable
/// calendarItemIdentifier — never title, list name, or array index.
func reminderToSourceRecord(_ reminder: EKReminder, observedAtISO8601: String) -> [String: Any] {
    var record: [String: Any] = [
        "externalId": reminder.calendarItemIdentifier,
        "sourceKind": sourceKindReminder,
        "observedAt": observedAtISO8601,
        "payload": reminderToPayload(reminder),
    ]
    if let sourceUpdatedAt = isoString(reminder.lastModifiedDate) {
        record["sourceUpdatedAt"] = sourceUpdatedAt
    }
    return record
}

enum SourceFetchOutcome {
    case success([[String: Any]])
    case failure(String)
}

/// performSourceFetch is M13 V1's deliberately simple full-current-read
/// policy (no incremental delta engine, no plugin-side durable cursor): it
/// always reads every current Reminder from EventKit and lets the Source
/// Layer's canonical-hash/SourceUpdatedAt dedupe decide what actually
/// changed. It never returns a nextCursor.
@MainActor
func performSourceFetch() async -> SourceFetchOutcome {
    var status = EKEventStore.authorizationStatus(for: .reminder)
    var granted = status == .authorized
    if #available(macOS 14.0, *) { granted = granted || status == .fullAccess }
    if !granted {
        let outcome = await requestFullAccess()
        // Re-check authorizationStatus rather than trusting outcome.granted
        // alone — the exact Slice 0 finding this plugin must not regress on.
        status = EKEventStore.authorizationStatus(for: .reminder)
        granted = status == .authorized
        if #available(macOS 14.0, *) { granted = granted || status == .fullAccess }
        if !granted {
            return .failure("reminders access not granted (authorizationStatus=\(authorizationStatusName(status)), requestGranted=\(outcome.granted))")
        }
    }
    let observedAtISO8601 = isoString(Date())!
    let reminders = await fetchAllReminders()
    let records = reminders.map { reminderToSourceRecord($0, observedAtISO8601: observedAtISO8601) }
    return .success(records)
}

func handleCapabilityInvoke(id: String, params: [String: Any]) async {
    guard let capabilityID = params["capabilityId"] as? String else {
        writeJSONLine(errorResponse(id: id, code: "invalid_invocation", message: "missing capabilityId"))
        return
    }
    guard capabilityID == sourceFetchCapability else {
        writeJSONLine(errorResponse(id: id, code: "capability_not_supported", message: "unsupported capability"))
        return
    }
    // params["input"] (SourceFetchRequest{cursor, context}) is intentionally
    // not inspected: this plugin has no incremental cursor semantics (see
    // performSourceFetch's doc comment) and accepts any input shape.
    switch await performSourceFetch() {
    case .success(let records):
        writeJSONLine(successResponse(id: id, result: ["records": records]))
    case .failure(let message):
        writeJSONLine(errorResponse(id: id, code: "source_fetch_failed", message: message))
    }
}

/// runPluginServer is the Host entry point: a synchronous, single-threaded
/// read-one-line / handle-it / write-one-line loop identical in shape to
/// lh-plugin-base/main.go's run(), so the Plugin Runtime V1 contract (one
/// process per extension, newline-JSON stdio, stdout=protocol only,
/// stderr=diagnostics) is satisfied with no wrapper process.
func runPluginServer() async {
    logStderr("started")
    while let line = readLine(strippingNewline: true) {
        guard let request = decodeRequestLine(line) else {
            // Malformed JSON on this one line only — keep serving further
            // lines, exactly like lh-plugin-base's decode-failure handling.
            continue
        }
        guard
            let id = request["id"] as? String, !id.isEmpty,
            let method = request["method"] as? String, !method.isEmpty
        else {
            // No usable id: matches lh-plugin-base's "ignored invalid
            // request: continue" — no response is possible without one.
            continue
        }
        let params = request["params"] as? [String: Any] ?? [:]
        switch method {
        case "system.handshake":
            handleHandshake(id: id, params: params)
        case "capability.invoke":
            await handleCapabilityInvoke(id: id, params: params)
        case "system.shutdown":
            writeJSONLine(successResponse(id: id, result: ["accepted": true]))
            logStderr("shutdown accepted")
            return
        default:
            writeJSONLine(errorResponse(id: id, code: "method_not_supported", message: "unsupported method"))
        }
    }
    logStderr("stdin closed")
}

// MARK: - Entry point

let arguments = CommandLine.arguments
let mode = arguments.count > 1 ? arguments[1] : "serve"

switch mode {
case "serve":
    await runPluginServer()

case "status":
    let status = EKEventStore.authorizationStatus(for: .reminder)
    print("{\"authorizationStatus\":\"\(authorizationStatusName(status))\"}")

case "request":
    let semaphoreResult = await requestFullAccess()
    let status = EKEventStore.authorizationStatus(for: .reminder)
    print("""
    {"granted":\(semaphoreResult.granted),"authorizationStatusAfter":"\(authorizationStatusName(status))","error":\(semaphoreResult.errorDescription.map { "\"\($0.replacingOccurrences(of: "\"", with: "'"))\"" } ?? "null")}
    """)

case "list":
    let statusBefore = EKEventStore.authorizationStatus(for: .reminder)
    var alreadyGranted = statusBefore == .authorized
    if #available(macOS 14.0, *) {
        alreadyGranted = alreadyGranted || statusBefore == .fullAccess
    }
    if !alreadyGranted {
        let result = await requestFullAccess()
        if !result.granted {
            fail("access not granted: \(result.errorDescription ?? "no additional error info"); authorizationStatus=\(authorizationStatusName(EKEventStore.authorizationStatus(for: .reminder)))")
        }
    }
    let reminders = await fetchAllReminders()
    let records = reminders.map(toRecord)
    let statusAfter = EKEventStore.authorizationStatus(for: .reminder)
    let output = ListResult(
        generatedAtISO8601: isoString(Date())!,
        authorizationStatus: authorizationStatusName(statusAfter),
        reminderCount: records.count,
        reminders: records
    )
    let encoder = JSONEncoder()
    encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
    let data = try! encoder.encode(output)
    print(String(data: data, encoding: .utf8)!)

default:
    fail("unknown mode \(mode); expected (no args, serve)|status|request|list")
}
