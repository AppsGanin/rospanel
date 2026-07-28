// Compile-time guarantee that every plural key carries every form its languages
// can select.
//
// This exists because of a real bug, not a hypothetical one. The dictionaries
// originally carried Russian-style _one/_few/_many everywhere. English's
// Intl.PluralRules only ever selects `one` and `other`, so for any count >= 2 the
// English UI found no _other form, fell through to fallbackLng, and rendered
// RUSSIAN text — "2 устройства" in an otherwise English panel, across sixteen
// different phrases. tsc could not see it: the two dictionaries had identical
// keys, which is exactly what the Dict type checks.
//
// So the invariant is raised to the type level: a key ending in _one obliges the
// same object to carry _few, _many and _other. Russian selects one/few/many for
// integers and other for fractions; English selects one/other. Requiring the union
// makes any supported language complete by construction, and a missing form is a
// compile error rather than a language switch away from being noticed.
//
// A missing form resolves the offending property to `never`, so its string literal
// fails to assign and tsc points straight at the key.
export type PluralComplete<T> = {
  [K in keyof T]: T[K] extends string
    ? K extends `${infer Base}_one`
      ? `${Base}_few` extends keyof T
        ? `${Base}_many` extends keyof T
          ? `${Base}_other` extends keyof T
            ? T[K]
            : never
          : never
        : never
      : T[K]
    : PluralComplete<T[K]>;
};
