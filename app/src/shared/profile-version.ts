import type { Profile } from "./generated/schema";

/**
 * The profile format version the app writes (PRO-01). The format was frozen
 * as version 1 in M14; files of version 0, the previews' format, are read as
 * they are and are never rewritten until the user edits them. The type comes
 * from schema/profile.schema.json, so this stops compiling when the schema
 * moves to a newer version, which points at every place that writes one.
 */
export const PROFILE_SCHEMA_VERSION: Profile["schemaVersion"] = 1;
