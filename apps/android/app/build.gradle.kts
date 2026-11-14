plugins { id("com.android.application") }
android {
    namespace = "kazuph.ikesu"
    compileSdk = 36
    defaultConfig {
        applicationId = "kazuph.ikesu"
        minSdk = 31
        targetSdk = 36
        versionCode = 2
        versionName = "0.2"
    }
}
dependencies {
    implementation("com.squareup.okhttp3:okhttp:4.12.0")
    testImplementation("junit:junit:4.13.2")
}
