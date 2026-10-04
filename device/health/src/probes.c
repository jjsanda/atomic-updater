/* probes.c — the health checks: app unit state and health.d probe scripts. */
#define _POSIX_C_SOURCE 200809L

#include "health.h"

#include <dirent.h>
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

bool check_app_unit(const struct ah_config *cfg, char *reason, size_t rsz)
{
    if (cfg->app_unit == NULL || cfg->app_unit[0] == '\0') {
        ah_log("app unit check disabled (ATOMIC_APP_UNIT empty)");
        return true;
    }
    char *argv[] = { "systemctl", "is-active", "--quiet",
                     (char *)cfg->app_unit, NULL };
    int rc = run_argv(argv);
    if (rc == 0) {
        ah_log("app unit %s active", cfg->app_unit);
        return true;
    }
    snprintf(reason, rsz, "app unit %s not active", cfg->app_unit);
    return false;
}

static int cmp_names(const void *a, const void *b)
{
    return strcmp(*(const char *const *)a, *(const char *const *)b);
}

static bool ends_with_sh(const char *name)
{
    size_t n = strlen(name);
    return n > 3 && strcmp(name + n - 3, ".sh") == 0;
}

bool run_probes(const struct ah_config *cfg, char *reason, size_t rsz)
{
    DIR *dir = opendir(cfg->health_dir);
    if (dir == NULL) {
        if (errno == ENOENT) {
            ah_log("probe dir %s missing, no probes to run", cfg->health_dir);
            return true;
        }
        snprintf(reason, rsz, "cannot open probe dir %s: %s", cfg->health_dir,
                 strerror(errno));
        return false;
    }

    char **names = NULL;
    size_t n = 0, cap = 0;
    struct dirent *de;
    while ((de = readdir(dir)) != NULL) {
        if (!ends_with_sh(de->d_name))
            continue;
        if (n == cap) {
            cap = cap ? cap * 2 : 16;
            char **grown = realloc(names, cap * sizeof(*names));
            if (grown == NULL) {
                ah_err("out of memory");
                exit(1);
            }
            names = grown;
        }
        names[n++] = ah_strdup(de->d_name);
    }
    closedir(dir);

    if (n == 0) {
        ah_log("no probes in %s", cfg->health_dir);
        free(names);
        return true;
    }
    qsort(names, n, sizeof(*names), cmp_names);

    bool ok = true;
    for (size_t i = 0; i < n && ok; i++) {
        char path[4096];
        if ((size_t)snprintf(path, sizeof(path), "%s/%s", cfg->health_dir,
                             names[i]) >= sizeof(path)) {
            snprintf(reason, rsz, "probe %s path too long", names[i]);
            ok = false;
            break;
        }
        ah_log("running probe %s (timeout %ds)", names[i],
               cfg->probe_timeout);
        int rc = run_probe(path, cfg->probe_timeout);
        if (rc == 0) {
            ah_log("probe %s passed", names[i]);
        } else if (rc == AH_PROBE_TIMEOUT) {
            snprintf(reason, rsz, "probe %s timeout", names[i]);
            ok = false;
        } else {
            snprintf(reason, rsz, "probe %s exit %d", names[i], rc);
            ok = false;
        }
    }

    for (size_t i = 0; i < n; i++)
        free(names[i]);
    free(names);
    return ok;
}
