import { z } from "zod";

// zod 4 words its default issue messages differently from zod 3 ("Too small:
// expected string to have >=1 characters" instead of "String must contain at
// least 1 character(s)"). Those messages are part of what the panel answers:
// a router's 400 carries error.flatten() (app/api/router/_lib.ts), a partner
// reads `path: message` (partner-api.ts), and vctl's own docs quote them. This
// error map words every issue zod 3 knew exactly as zod 3 did; a message a
// schema sets itself still wins over it. Anything zod 3 had no word for keeps
// zod 4's.
//
// z.config is global to the zod module, so importing @vectra/contracts is
// enough for every schema in the process, the panel's own included.
//
// Not covered, because no schema here produces them: a record whose key
// schema can fail (zod 4 says "Invalid key in record") and a numeric enum.

type RawIssue = z.core.$ZodRawIssue;

// zod 3's name for the type of a value (getParsedType).
function receivedType(input: unknown) {
  switch (typeof input) {
    case "number":
      return Number.isNaN(input) ? "nan" : "number";
    case "object":
      if (input === null) return "null";
      if (Array.isArray(input)) return "array";
      if (
        typeof (input as { then?: unknown }).then === "function" &&
        typeof (input as { catch?: unknown }).catch === "function"
      ) {
        return "promise";
      }
      if (input instanceof Map) return "map";
      if (input instanceof Set) return "set";
      if (input instanceof Date) return "date";
      return "object";
    default:
      return typeof input;
  }
}

function joinValues(values: readonly unknown[], separator = " | ") {
  return values
    .map((value) => (typeof value === "string" ? `'${value}'` : value))
    .join(separator);
}

// zod 4 names a few expected types zod 3 called otherwise.
const EXPECTED_TYPE: Record<string, string> = {
  record: "object",
  tuple: "array",
};

function invalidTypeMessage(issue: RawIssue & { code: "invalid_type" }) {
  if (issue.expected === "date" && issue.input instanceof Date) {
    return "Invalid date";
  }
  // .int() on a fraction: zod 3 reported it as a float.
  if (issue.expected === "int") {
    return "Expected integer, received float";
  }
  const received = receivedType(issue.input);
  if (received === "undefined") {
    return "Required";
  }
  return `Expected ${EXPECTED_TYPE[issue.expected] ?? issue.expected}, received ${received}`;
}

function invalidValueMessage(issue: RawIssue & { code: "invalid_value" }) {
  if ((issue.inst?._zod.def as { type?: string } | undefined)?.type === "literal") {
    return `Invalid literal value, expected ${JSON.stringify(issue.values[0])}`;
  }
  const received = receivedType(issue.input);
  if (received === "undefined") {
    return "Required";
  }
  if (received !== "string") {
    return `Expected ${joinValues(issue.values)}, received ${received}`;
  }
  return `Invalid enum value. Expected ${joinValues(issue.values)}, received '${String(issue.input)}'`;
}

// zod 3 called a failed string format by its own check name.
const FORMAT_NAME: Record<string, string> = {
  guid: "uuid",
  ipv4: "ip",
  ipv6: "ip",
  cidrv4: "cidr",
  cidrv6: "cidr",
};

function invalidFormatMessage(issue: RawIssue & { code: "invalid_format" }) {
  switch (issue.format) {
    case "regex":
      return "Invalid";
    case "starts_with":
      return `Invalid input: must start with "${(issue as { prefix?: string }).prefix}"`;
    case "ends_with":
      return `Invalid input: must end with "${(issue as { suffix?: string }).suffix}"`;
    case "includes":
      return `Invalid input: must include "${(issue as { includes?: string }).includes}"`;
    default:
      return `Invalid ${FORMAT_NAME[issue.format] ?? issue.format}`;
  }
}

function sizeMessage(
  issue: RawIssue & { code: "too_small" | "too_big" },
): string | undefined {
  const small = issue.code === "too_small";
  const bound = small
    ? (issue as { minimum: number | bigint }).minimum
    : (issue as { maximum: number | bigint }).maximum;
  const { exact, inclusive } = issue as { exact?: boolean; inclusive?: boolean };
  const pick = (exactly: string, atBound: string, past: string) =>
    exact ? exactly : inclusive ? atBound : past;
  switch (issue.origin) {
    case "array":
      return `Array must contain ${pick("exactly", small ? "at least" : "at most", small ? "more than" : "less than")} ${bound} element(s)`;
    case "string":
      return `String must contain ${pick("exactly", small ? "at least" : "at most", small ? "over" : "under")} ${bound} character(s)`;
    case "number":
    case "int":
      return small
        ? `Number must be ${pick("exactly equal to ", "greater than or equal to ", "greater than ")}${bound}`
        : `Number must be ${pick("exactly", "less than or equal to", "less than")} ${bound}`;
    case "bigint":
      return small
        ? `Number must be ${pick("exactly equal to ", "greater than or equal to ", "greater than ")}${bound}`
        : `BigInt must be ${pick("exactly", "less than or equal to", "less than")} ${bound}`;
    case "date":
      return small
        ? `Date must be ${pick("exactly equal to ", "greater than or equal to ", "greater than ")}${new Date(Number(bound))}`
        : `Date must be ${pick("exactly", "smaller than or equal to", "smaller than")} ${new Date(Number(bound))}`;
    default:
      return "Invalid input";
  }
}

export function zodV3Message(issue: RawIssue): string | undefined {
  switch (issue.code) {
    case "invalid_type":
      return invalidTypeMessage(issue);
    case "invalid_value":
      return invalidValueMessage(issue);
    case "invalid_format":
      return invalidFormatMessage(issue);
    case "too_small":
    case "too_big":
      return sizeMessage(issue);
    case "unrecognized_keys":
      return `Unrecognized key(s) in object: ${joinValues(issue.keys, ", ")}`;
    case "invalid_union": {
      const options = (issue as { options?: unknown[] }).options;
      return (issue as { note?: string }).note === "No matching discriminator" &&
        options
        ? `Invalid discriminator value. Expected ${joinValues(options)}`
        : "Invalid input";
    }
    case "not_multiple_of":
      return `Number must be a multiple of ${String(issue.divisor)}`;
    case "custom":
      return "Invalid input";
    default:
      return undefined;
  }
}

z.config({ customError: zodV3Message });

// zod 4 still measures a value's length after its type check failed: an array
// where a string belongs comes back with "Expected string, received array"
// AND "Array must contain at least 1 element(s)" (or the schema's own min
// message). zod 3 stopped at the type. Wherever issues leave the panel they go
// through this, so the list is zod 3's again.
function pathKey(path: readonly PropertyKey[]) {
  return path.map(String).join("\u0000");
}

export function zodV3Issues<T extends z.core.$ZodIssue>(issues: readonly T[]) {
  const mistyped = new Set(
    issues
      .filter((issue) => issue.code === "invalid_type" || issue.code === "invalid_value")
      .map((issue) => pathKey(issue.path)),
  );
  return issues.filter(
    (issue) =>
      !(
        (issue.code === "too_small" || issue.code === "too_big") &&
        mistyped.has(pathKey(issue.path))
      ),
  );
}

export function zodV3Error(error: z.ZodError) {
  return new z.ZodError(zodV3Issues(error.issues));
}
