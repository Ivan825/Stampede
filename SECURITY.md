# Security policy

## Reporting a vulnerability

Please do not open a public issue. Report it privately through GitHub:
**Security → Report a vulnerability** on
[github.com/Ivan825/Stampede](https://github.com/Ivan825/Stampede/security/advisories/new).

Include what you found, how to reproduce it, and the impact you expect. You
will get an acknowledgement within 3 working days and a status update at
least every 7 days until it is resolved. Fixed issues are credited in the
release notes unless you ask otherwise.

## Supported versions

Until v1.0, only the latest commit on `main` receives fixes. From v1.0, the
latest minor release receives security fixes.

## Scope

Of particular interest:

- ways to make Stampede send load to a host the user has not verified or
  allowed (bypassing the target policy or caps);
- a scenario file that can read local files, environment variables or
  secrets beyond what the user granted, or execute code;
- secrets leaking into reports, logs or exported files.

Stampede is a load generator. Using it against systems you do not own is
misuse, not a vulnerability.
