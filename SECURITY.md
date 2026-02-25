# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in yoloai, please report it responsibly.

**Email:** kstenerud@gmail.com

Please include:
- Description of the vulnerability
- Steps to reproduce
- Potential impact

I will acknowledge receipt within 48 hours and aim to provide a fix or mitigation plan within 7 days.

## Scope

yoloai runs AI coding agents inside Docker containers and handles credential injection. Security issues in the following areas are especially relevant:
- Container escape or privilege escalation
- Credential leakage (API keys, tokens)
- Host filesystem access beyond intended mounts
- Network isolation bypasses

## Supported Versions

Only the latest release is supported with security updates.
