plugins { id("com.android.application") }
android {
    namespace = "kazuph.ikesu"
    compileSdk = 36
    defaultConfig {
        applicationId = "kazuph.ikesu"
        minSdk = 31
        targetSdk = 36
        versionCode = 8
        versionName = "0.8"
    }
}
dependencies {
    implementation("com.squareup.okhttp3:okhttp:4.12.0")
    implementation("org.signal.forks:noise-java:0.1.1")
    implementation("com.journeyapps:zxing-android-embedded:4.3.0")
    implementation("com.github.termux.termux-app:terminal-view:v0.118.3")
    implementation("com.github.termux.termux-app:terminal-emulator:v0.118.3")
    testImplementation("junit:junit:4.13.2")
    testImplementation("org.json:json:20240303")
}
