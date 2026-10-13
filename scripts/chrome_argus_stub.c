// chrome_argus_stub.c — launcher stub for Chrome Argus.app (argus#18).
// Resolves the bundled "Google Chrome Framework" under Frameworks/.../Versions/*
// dynamically (no hardcoded version), dlopens it, and jumps into ChromeMain.
// Build: see scripts/rebuild_chrome_argus.sh.
#include <dlfcn.h>
#include <mach-o/dyld.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <dirent.h>
#include <limits.h>
#include <unistd.h>
#include <libgen.h>

typedef int (*ChromeMainFn)(int argc, char** argv);

static int find_framework(char* out, size_t outsz) {
    char exe[PATH_MAX];
    uint32_t sz = sizeof(exe);
    if (_NSGetExecutablePath(exe, &sz) != 0) return -1;
    char resolved[PATH_MAX];
    if (!realpath(exe, resolved)) return -1;
    // exe is <bundle>/Contents/MacOS/<name>; framework dir is <bundle>/Contents/Frameworks/...
    char* macos = dirname(resolved);            // .../Contents/MacOS
    char* contents = dirname(macos);            // .../Contents
    char versions_dir[PATH_MAX];
    snprintf(versions_dir, sizeof(versions_dir),
             "%s/Frameworks/Google Chrome Framework.framework/Versions", contents);
    DIR* d = opendir(versions_dir);
    if (!d) return -1;
    struct dirent* e;
    char best[PATH_MAX] = {0};
    while ((e = readdir(d)) != NULL) {
        if (e->d_name[0] == '.') continue;
        char candidate[PATH_MAX];
        snprintf(candidate, sizeof(candidate), "%s/%s/Google Chrome Framework",
                 versions_dir, e->d_name);
        if (access(candidate, R_OK) == 0 && strcmp(candidate, best) > 0) {
            strncpy(best, candidate, sizeof(best) - 1);
        }
    }
    closedir(d);
    if (!best[0]) return -1;
    strncpy(out, best, outsz - 1);
    return 0;
}

int main(int argc, char** argv) {
    char fw[PATH_MAX];
    if (find_framework(fw, sizeof(fw)) != 0) {
        fprintf(stderr, "chrome-argus: Google Chrome Framework not found under Contents/Frameworks\n");
        return 1;
    }
    void* h = dlopen(fw, RTLD_NOW | RTLD_GLOBAL);
    if (!h) {
        fprintf(stderr, "chrome-argus: dlopen %s: %s\n", fw, dlerror());
        return 1;
    }
    ChromeMainFn main_fn = (ChromeMainFn)dlsym(h, "ChromeMain");
    if (!main_fn) {
        fprintf(stderr, "chrome-argus: dlsym ChromeMain: %s\n", dlerror());
        return 1;
    }
    return main_fn(argc, argv);
}
