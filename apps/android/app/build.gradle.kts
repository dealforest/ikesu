plugins { id("com.android.application") }
android {
    // Termux の libtermux.so は端末の中で手元のプログラムを動かす部品（JNI クラスだけが読む）。このアプリは画面を描くだけで使わない。
    // 16 KB ページの端末（Pixel 10 など）で揃っていない警告が出るので、入れない。
    packaging { jniLibs { excludes += "**/libtermux.so" } }
    namespace = "kazuph.ikesu"
    compileSdk = 36
    defaultConfig {
        applicationId = "kazuph.ikesu"
        minSdk = 31
        targetSdk = 36
        versionCode = 29
        versionName = "0.29"
    }
}
dependencies {
    implementation("com.squareup.okhttp3:okhttp:4.12.0")
    implementation("org.signal.forks:noise-java:0.1.1")
    implementation("com.journeyapps:zxing-android-embedded:4.3.0")
    implementation("androidx.core:core:1.13.1")
    implementation("com.github.termux.termux-app:terminal-view:v0.118.3")
    implementation("com.github.termux.termux-app:terminal-emulator:v0.118.3")
    testImplementation("junit:junit:4.13.2")
    testImplementation("org.json:json:20240303")
}
