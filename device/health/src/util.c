/* util.c — logging, small string helpers, kernel cmdline parsing. */
#define _POSIX_C_SOURCE 200809L

#include "health.h"

#include <ctype.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define AH_PREFIX "atomic-health: "

static void vlogto(FILE *stream, const char *fmt, va_list ap)
{
    fputs(AH_PREFIX, stream);
    vfprintf(stream, fmt, ap);
    fputc('\n', stream);
    fflush(stream);
}

void ah_log(const char *fmt, ...)
{
    va_list ap;
    va_start(ap, fmt);
    vlogto(stdout, fmt, ap);
    va_end(ap);
}

void ah_err(const char *fmt, ...)
{
    va_list ap;
    va_start(ap, fmt);
    vlogto(stderr, fmt, ap);
    va_end(ap);
}

char *ah_strdup(const char *s)
{
    char *d = strdup(s);
    if (d == NULL) {
        ah_err("out of memory");
        exit(1);
    }
    return d;
}

int ah_split(char *s, char **argv, int max)
{
    int n = 0;
    while (*s != '\0' && n < max - 1) {
        while (*s != '\0' && isspace((unsigned char)*s))
            *s++ = '\0';
        if (*s == '\0')
            break;
        argv[n++] = s;
        while (*s != '\0' && !isspace((unsigned char)*s))
            s++;
    }
    argv[n] = NULL;
    return n;
}

bool ah_parse_cmdline_slot(const char *path, char *out, size_t outsz)
{
    static const char key[] = "rauc.slot=";
    char buf[4096];
    FILE *f = fopen(path, "r");
    if (f == NULL)
        return false;
    size_t n = fread(buf, 1, sizeof(buf) - 1, f);
    fclose(f);
    buf[n] = '\0';

    const char *p = buf;
    while ((p = strstr(p, key)) != NULL) {
        if (p == buf || isspace((unsigned char)p[-1])) {
            p += sizeof(key) - 1;
            size_t i = 0;
            while (p[i] != '\0' && !isspace((unsigned char)p[i]) &&
                   i + 1 < outsz) {
                out[i] = p[i];
                i++;
            }
            out[i] = '\0';
            return i > 0;
        }
        p += sizeof(key) - 1;
    }
    return false;
}
