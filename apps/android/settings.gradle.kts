pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}

dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google()
        mavenCentral()
        // Termux の端末の部品（terminal-emulator・terminal-view、Apache 2.0）は JitPack にだけある。この群だけ取る。
        maven("https://jitpack.io") { content { includeGroup("com.github.termux.termux-app") } }
    }
}

rootProject.name = "ikesu"
include(":app")
