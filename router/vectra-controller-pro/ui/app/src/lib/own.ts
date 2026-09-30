/** `obj[key]` only when it is obj's own property: router and provider data can name keys like "__proto__". */
export const own = <V>(obj: Readonly<Record<string, V>>, key: string): V | undefined =>
  Object.prototype.hasOwnProperty.call(obj, key) ? obj[key] : undefined;
